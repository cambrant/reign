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
