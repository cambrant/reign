package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DockerClient talks to the Docker Engine API over its unix socket using only
// the standard library. It replaces CLI calls where one API request can answer
// for every project at once.
type DockerClient struct {
	http    *http.Client
	baseURL string
}

// NewDockerClient returns a client for the Docker socket at path
// (usually /var/run/docker.sock).
func NewDockerClient(socketPath string) *DockerClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &DockerClient{
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
		// The host is ignored by the unix-socket dialer but required in the URL.
		baseURL: "http://docker",
	}
}

// get performs a GET request and decodes the JSON response into v.
func (c *DockerClient) get(ctx context.Context, path string, query url.Values, v any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker API %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker API %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("docker API %s: decode: %w", path, err)
	}
	return nil
}

// apiContainer is one entry of GET /containers/json.
type apiContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

// Compose labels on containers.
const (
	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelOneOff  = "com.docker.compose.oneoff"
)

// ComposeContainers lists all compose-managed containers (running or not),
// grouped by compose project name, in one API request.
func (c *DockerClient) ComposeContainers(ctx context.Context) (map[string][]psEntry, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {labelProject}})
	var list []apiContainer
	if err := c.get(ctx, "/containers/json", url.Values{"all": {"1"}, "filters": {string(filters)}}, &list); err != nil {
		return nil, err
	}
	byProject := make(map[string][]psEntry)
	for _, ac := range list {
		project := ac.Labels[labelProject]
		byProject[project] = append(byProject[project], ac.psEntry())
	}
	return byProject, nil
}

var (
	exitCodePattern = regexp.MustCompile(`^Exited \((-?\d+)\)`)
)

// psEntry converts an API container to the shape `compose ps` produces, so
// both sources feed the same evaluateStatus.
func (ac apiContainer) psEntry() psEntry {
	p := psEntry{
		Service: ac.Labels[labelService],
		State:   ac.State,
		Health:  healthFromStatus(ac.Status),
		Labels:  labelOneOff + "=" + ac.Labels[labelOneOff],
	}
	if m := exitCodePattern.FindStringSubmatch(ac.Status); m != nil {
		p.ExitCode, _ = strconv.Atoi(m[1])
	}
	return p
}

// healthFromStatus extracts the health state from a status like
// "Up 2 hours (healthy)" or "Up 5 seconds (health: starting)".
func healthFromStatus(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	}
	return ""
}

// ContainerEvent is a container lifecycle event of a compose project.
type ContainerEvent struct {
	Project string // com.docker.compose.project label
	Action  string // die, stop, kill, oom, start, health_status: unhealthy, ...
	Name    string // container name
}

// watchedActions are the container events that can change a project's status.
var watchedActions = []string{"start", "die", "stop", "kill", "oom", "health_status"}

// Events streams container events of compose projects to onEvent until ctx
// is cancelled or the stream breaks; it returns the reason.
func (c *DockerClient) Events(ctx context.Context, onEvent func(ContainerEvent)) error {
	filters, _ := json.Marshal(map[string][]string{
		"type":  {"container"},
		"event": watchedActions,
		"label": {labelProject},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/events?"+url.Values{"filters": {string(filters)}}.Encode(), nil)
	if err != nil {
		return err
	}
	// The stream stays open indefinitely: no client timeout.
	stream := &http.Client{Transport: c.http.Transport}
	resp, err := stream.Do(req)
	if err != nil {
		return fmt.Errorf("docker events: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker events: %s", resp.Status)
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var ev struct {
			Action string `json:"Action"`
			Actor  struct {
				Attributes map[string]string `json:"Attributes"`
			} `json:"Actor"`
		}
		if err := dec.Decode(&ev); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("docker events: %w", err)
		}
		onEvent(ContainerEvent{
			Project: ev.Actor.Attributes[labelProject],
			Action:  ev.Action,
			Name:    ev.Actor.Attributes["name"],
		})
	}
}
