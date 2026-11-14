// Package doctor finds things on the host that reign should manage but does
// not: compose projects outside reign, and systemd units still running
// `docker compose` for paths reign manages.
package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

// ProjectLister lists the compose projects on the Docker host.
type ProjectLister interface {
	ComposeProjects(ctx context.Context) ([]executor.ComposeProject, error)
}

// Runner runs a command and returns its stdout.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Doctor inspects the host.
type Doctor struct {
	Projects ProjectLister // nil skips the compose project check
	Run      Runner        // runs systemctl
	UnitDir  string        // where administrator units live (default /etc/systemd/system)
}

// Report is the result of a check.
type Report struct {
	UnmanagedProjects []executor.ComposeProject `json:"unmanaged_projects"`
	ConflictingUnits  []Unit                    `json:"conflicting_units"`
	Warnings          []string                  `json:"warnings,omitempty"`
}

// Issues returns the number of findings.
func (r Report) Issues() int {
	return len(r.UnmanagedProjects) + len(r.ConflictingUnits)
}

// Unit is a systemd unit that runs docker compose for a reign-managed path.
type Unit struct {
	Name      string `json:"name"`
	File      string `json:"file"`
	ServiceID string `json:"service_id"`
	Path      string `json:"path"`
	Enabled   string `json:"enabled"`  // UnitFileState: enabled, disabled, ...
	Active    string `json:"active"`   // ActiveState: active, inactive, failed, ...
	Restarts  int    `json:"restarts"` // NRestarts since the unit was loaded
}

// Check runs all checks against the given reign services.
func (d *Doctor) Check(ctx context.Context, services []models.Service) Report {
	var r Report
	paths := composePaths(services)

	if d.Projects != nil {
		projects, err := d.Projects.ComposeProjects(ctx)
		if err != nil {
			r.Warnings = append(r.Warnings, "could not list compose projects: "+err.Error())
		} else {
			r.UnmanagedProjects = unmanagedProjects(projects, paths)
		}
	}

	units, err := d.conflictingUnits(ctx, paths)
	if err != nil {
		r.Warnings = append(r.Warnings, "could not scan systemd units: "+err.Error())
	}
	r.ConflictingUnits = units

	if r.UnmanagedProjects == nil {
		r.UnmanagedProjects = []executor.ComposeProject{}
	}
	if r.ConflictingUnits == nil {
		r.ConflictingUnits = []Unit{}
	}
	return r
}

// composePaths maps each compose service's normalized path to its ID.
func composePaths(services []models.Service) map[string]string {
	paths := map[string]string{}
	for _, s := range services {
		if s.Type == models.ServiceTypeCompose && s.Path != "" {
			paths[normalize(s.Path)] = s.ID
		}
	}
	return paths
}

// normalize cleans a path and resolves symlinks where possible, so
// /home/tim/app and /home/tim/./app/ compare equal.
func normalize(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// unmanagedProjects returns projects whose working directory is no reign service's path.
func unmanagedProjects(projects []executor.ComposeProject, paths map[string]string) []executor.ComposeProject {
	var out []executor.ComposeProject
	for _, p := range projects {
		if _, managed := paths[normalize(p.WorkingDir)]; !managed {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// conflictingUnits finds unit files that run docker compose for a path
// reign manages, and asks systemd for their state.
func (d *Doctor) conflictingUnits(ctx context.Context, paths map[string]string) ([]Unit, error) {
	dir := d.UnitDir
	if dir == "" {
		dir = "/etc/systemd/system"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var units []Unit
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".service") || strings.HasPrefix(e.Name(), "reign") {
			continue
		}
		file := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(file) // follows symlinks to the unit's real file
		if err != nil || !runsCompose(data) {
			continue
		}
		if id, path, ok := matchPath(data, paths); ok {
			units = append(units, Unit{Name: e.Name(), File: file, ServiceID: id, Path: path})
		}
	}
	if len(units) > 0 && d.Run != nil {
		d.fillUnitState(ctx, units)
	}
	return units, nil
}

// runsCompose reports whether a unit file's Exec lines call docker compose.
func runsCompose(unit []byte) bool {
	for _, line := range strings.Split(string(unit), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Exec") {
			continue
		}
		if strings.Contains(line, "docker compose") || strings.Contains(line, "docker-compose") {
			return true
		}
	}
	return false
}

// matchPath finds a reign-managed path mentioned in the unit file, as a whole
// path (followed by a separator, quote, space or end of line).
func matchPath(unit []byte, paths map[string]string) (id, path string, ok bool) {
	text := string(unit)
	// Longest paths first, so /srv/app-db is not reported as /srv/app.
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, p := range keys {
		for i := strings.Index(text, p); i >= 0; {
			end := i + len(p)
			if end == len(text) || strings.ContainsRune("/ \t\n\"';&", rune(text[end])) {
				return paths[p], p, true
			}
			next := strings.Index(text[end:], p)
			if next < 0 {
				break
			}
			i = end + next
		}
	}
	return "", "", false
}

// fillUnitState asks systemd for each unit's enablement, state and restart count.
func (d *Doctor) fillUnitState(ctx context.Context, units []Unit) {
	args := []string{"show", "-p", "Id", "-p", "UnitFileState", "-p", "ActiveState", "-p", "NRestarts"}
	for _, u := range units {
		args = append(args, u.Name)
	}
	out, err := d.Run(ctx, "systemctl", args...)
	if err != nil {
		return
	}
	byName := map[string]map[string]string{}
	for _, block := range bytes.Split(out, []byte("\n\n")) {
		props := map[string]string{}
		for _, line := range strings.Split(string(block), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				props[k] = v
			}
		}
		if props["Id"] != "" {
			byName[props["Id"]] = props
		}
	}
	for i := range units {
		props := byName[units[i].Name]
		units[i].Enabled = props["UnitFileState"]
		units[i].Active = props["ActiveState"]
		units[i].Restarts, _ = strconv.Atoi(props["NRestarts"])
	}
}
