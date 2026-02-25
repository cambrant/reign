package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ListCommand handles the 'list' subcommand.
type ListCommand struct {
	client *Client
	json   bool
}

// NewListCommand creates a new list command.
func NewListCommand(client *Client, jsonOutput bool) *ListCommand {
	return &ListCommand{
		client: client,
		json:   jsonOutput,
	}
}

// Run executes the list command.
func (c *ListCommand) Run() error {
	resp, err := c.client.ListServices()
	if err != nil {
		return err
	}

	if c.json {
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal response: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if len(resp.Services) == 0 {
		fmt.Println("No services found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tTYPE\tSTATUS\tENABLED\tINFRA")
	for _, s := range resp.Services {
		enabled := "yes"
		if !s.Enabled {
			enabled = "no"
		}
		infra := ""
		if s.Infrastructure {
			infra = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID, s.Name, s.Type, s.State.Status, enabled, infra)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d service(s)\n", resp.Total)
	return nil
}

// ShowCommand handles the 'show' subcommand.
type ShowCommand struct {
	client    *Client
	serviceID string
	json      bool
}

// NewShowCommand creates a new show command.
func NewShowCommand(client *Client, serviceID string, jsonOutput bool) *ShowCommand {
	return &ShowCommand{
		client:    client,
		serviceID: serviceID,
		json:      jsonOutput,
	}
}

// Run executes the show command.
func (c *ShowCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	resp, err := c.client.GetService(c.serviceID)
	if err != nil {
		return err
	}

	if c.json {
		// For JSON output, only output the service definition (for use with create/update)
		serviceOnly := struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Type           string `json:"type"`
			Path           string `json:"path"`
			Command        string `json:"command,omitempty"`
			Enabled        bool   `json:"enabled"`
			Infrastructure bool   `json:"infrastructure"`
		}{
			ID:             resp.ID,
			Name:           resp.Name,
			Type:           resp.Type,
			Path:           resp.Path,
			Command:        resp.Command,
			Enabled:        resp.Enabled,
			Infrastructure: resp.Infrastructure,
		}
		data, err := json.MarshalIndent(serviceOnly, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal response: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	// Human-readable output
	fmt.Printf("Service: %s\n", resp.ID)
	fmt.Printf("  Name:           %s\n", resp.Name)
	fmt.Printf("  Type:           %s\n", resp.Type)
	fmt.Printf("  Path:           %s\n", resp.Path)
	if resp.Command != "" {
		fmt.Printf("  Command:        %s\n", resp.Command)
	}
	fmt.Printf("  Enabled:        %v\n", resp.Enabled)
	fmt.Printf("  Infrastructure: %v\n", resp.Infrastructure)
	fmt.Printf("  Created:        %s\n", resp.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Updated:        %s\n", resp.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Println()

	// State information
	fmt.Printf("State:\n")
	fmt.Printf("  Status:         %s\n", resp.State.Status)
	if resp.State.PID != nil {
		fmt.Printf("  PID:            %d\n", *resp.State.PID)
	}
	if resp.State.StartedAt != nil {
		fmt.Printf("  Started:        %s\n", resp.State.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if resp.State.StoppedAt != nil {
		fmt.Printf("  Stopped:        %s\n", resp.State.StoppedAt.Format("2006-01-02 15:04:05"))
	}
	if resp.State.RestartCount > 0 {
		fmt.Printf("  Restart Count:  %d\n", resp.State.RestartCount)
	}
	if resp.State.LastError != nil {
		fmt.Printf("  Last Error:     %s\n", *resp.State.LastError)
	}

	// Recent events
	if len(resp.Events) > 0 {
		fmt.Println()
		fmt.Println("Recent Events:")
		for _, e := range resp.Events {
			msg := ""
			if e.Message != "" {
				msg = " - " + e.Message
			}
			fmt.Printf("  [%s] %s%s\n", e.CreatedAt.Format("2006-01-02 15:04:05"), e.EventType, msg)
		}
	}

	return nil
}

// DumpCommand handles the 'dump' subcommand.
type DumpCommand struct {
	client    *Client
	serviceID string
}

// NewDumpCommand creates a new dump command.
func NewDumpCommand(client *Client, serviceID string) *DumpCommand {
	return &DumpCommand{
		client:    client,
		serviceID: serviceID,
	}
}

// Run executes the dump command.
func (c *DumpCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	resp, err := c.client.GetService(c.serviceID)
	if err != nil {
		return err
	}

	serviceDef := struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Path           string `json:"path"`
		Command        string `json:"command,omitempty"`
		Enabled        bool   `json:"enabled"`
		Infrastructure bool   `json:"infrastructure"`
	}{
		ID:             resp.ID,
		Name:           resp.Name,
		Type:           resp.Type,
		Path:           resp.Path,
		Command:        resp.Command,
		Enabled:        resp.Enabled,
		Infrastructure: resp.Infrastructure,
	}

	data, err := json.Marshal(serviceDef)
	if err != nil {
		return fmt.Errorf("failed to marshal service: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// StatusCommand handles the 'status' subcommand (alias for list).
type StatusCommand struct {
	*ListCommand
}

// NewStatusCommand creates a new status command.
func NewStatusCommand(client *Client, jsonOutput bool) *StatusCommand {
	return &StatusCommand{
		ListCommand: NewListCommand(client, jsonOutput),
	}
}

// ActionCommand handles start/stop/restart subcommands.
type ActionCommand struct {
	client    *Client
	action    string
	serviceID string
}

// NewActionCommand creates a new action command.
func NewActionCommand(client *Client, action, serviceID string) *ActionCommand {
	return &ActionCommand{
		client:    client,
		action:    action,
		serviceID: serviceID,
	}
}

// Run executes the action command.
func (c *ActionCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	var svc *ServiceWithState
	var err error

	switch c.action {
	case "start":
		svc, err = c.client.StartService(c.serviceID)
	case "stop":
		svc, err = c.client.StopService(c.serviceID)
	case "restart":
		svc, err = c.client.RestartService(c.serviceID)
	default:
		return fmt.Errorf("unknown action: %s", c.action)
	}

	if err != nil {
		return err
	}

	status := svc.State.Status
	switch status {
	case "starting":
		fmt.Printf("Service '%s' is starting\n", svc.ID)
	case "stopping":
		fmt.Printf("Service '%s' is stopping\n", svc.ID)
	default:
		fmt.Printf("Service '%s' %s: %s\n", svc.ID, getActionPastTense(c.action), status)
	}
	return nil
}

func getActionPastTense(action string) string {
	switch action {
	case "start":
		return "started"
	case "stop":
		return "stopped"
	case "restart":
		return "restarted"
	default:
		return action + "ed"
	}
}

// CreateCommand handles the 'create' subcommand.
type CreateCommand struct {
	client  *Client
	options *CreateUpdateOptions
}

// CreateUpdateOptions holds options for create/update commands.
type CreateUpdateOptions struct {
	// From JSON file or stdin
	JSONFile string
	JSONData string

	// Individual flags
	ID             string
	Name           string
	Type           string
	Path           string
	Command        string
	Enabled        *bool
	Infrastructure *bool
}

// NewCreateCommand creates a new create command.
func NewCreateCommand(client *Client, opts *CreateUpdateOptions) *CreateCommand {
	return &CreateCommand{
		client:  client,
		options: opts,
	}
}

// Run executes the create command.
func (c *CreateCommand) Run() error {
	req, err := c.buildRequest()
	if err != nil {
		return err
	}

	// Validate required fields for create
	if req.ID == "" {
		return fmt.Errorf("id is required (use --id or provide in JSON)")
	}
	if req.Name == "" {
		return fmt.Errorf("name is required (use --name or provide in JSON)")
	}
	if req.Type == "" {
		return fmt.Errorf("type is required (use --type or provide in JSON)")
	}
	if req.Type != "compose" && req.Type != "binary" {
		return fmt.Errorf("type must be 'compose' or 'binary'")
	}
	if req.Path == "" {
		return fmt.Errorf("path is required (use --path or provide in JSON)")
	}

	svc, err := c.client.CreateService(req)
	if err != nil {
		return err
	}

	fmt.Printf("Created service '%s' (%s)\n", svc.ID, svc.Name)
	return nil
}

func (c *CreateCommand) buildRequest() (*ServiceRequest, error) {
	var req ServiceRequest

	// Load from JSON if provided
	if c.options.JSONFile != "" {
		data, err := os.ReadFile(c.options.JSONFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read JSON file: %w", err)
		}
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, fmt.Errorf("failed to parse JSON file: %w", err)
		}
	} else if c.options.JSONData != "" {
		if err := json.Unmarshal([]byte(c.options.JSONData), &req); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
	}

	// Override with individual flags (they take precedence)
	if c.options.ID != "" {
		req.ID = c.options.ID
	}
	if c.options.Name != "" {
		req.Name = c.options.Name
	}
	if c.options.Type != "" {
		req.Type = c.options.Type
	}
	if c.options.Path != "" {
		req.Path = c.options.Path
	}
	if c.options.Command != "" {
		req.Command = c.options.Command
	}
	if c.options.Enabled != nil {
		req.Enabled = c.options.Enabled
	}
	if c.options.Infrastructure != nil {
		req.Infrastructure = c.options.Infrastructure
	}

	return &req, nil
}

// UpdateCommand handles the 'update' subcommand.
type UpdateCommand struct {
	client    *Client
	serviceID string
	options   *CreateUpdateOptions
}

// NewUpdateCommand creates a new update command.
func NewUpdateCommand(client *Client, serviceID string, opts *CreateUpdateOptions) *UpdateCommand {
	return &UpdateCommand{
		client:    client,
		serviceID: serviceID,
		options:   opts,
	}
}

// Run executes the update command.
func (c *UpdateCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	req, err := c.buildRequest()
	if err != nil {
		return err
	}

	// Check that at least one field is being updated
	if req.Name == "" && req.Type == "" && req.Path == "" &&
		req.Command == "" && req.Enabled == nil && req.Infrastructure == nil {
		return fmt.Errorf("at least one field must be provided to update")
	}

	svc, err := c.client.UpdateService(c.serviceID, req)
	if err != nil {
		return err
	}

	fmt.Printf("Updated service '%s' (%s)\n", svc.ID, svc.Name)
	return nil
}

func (c *UpdateCommand) buildRequest() (*ServiceRequest, error) {
	var req ServiceRequest

	// Load from JSON if provided
	if c.options.JSONFile != "" {
		data, err := os.ReadFile(c.options.JSONFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read JSON file: %w", err)
		}
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, fmt.Errorf("failed to parse JSON file: %w", err)
		}
	} else if c.options.JSONData != "" {
		if err := json.Unmarshal([]byte(c.options.JSONData), &req); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
	}

	// Override with individual flags
	if c.options.Name != "" {
		req.Name = c.options.Name
	}
	if c.options.Type != "" {
		req.Type = c.options.Type
	}
	if c.options.Path != "" {
		req.Path = c.options.Path
	}
	if c.options.Command != "" {
		req.Command = c.options.Command
	}
	if c.options.Enabled != nil {
		req.Enabled = c.options.Enabled
	}
	if c.options.Infrastructure != nil {
		req.Infrastructure = c.options.Infrastructure
	}

	return &req, nil
}

// DeleteCommand handles the 'delete' subcommand.
type DeleteCommand struct {
	client    *Client
	serviceID string
	force     bool
}

// NewDeleteCommand creates a new delete command.
func NewDeleteCommand(client *Client, serviceID string, force bool) *DeleteCommand {
	return &DeleteCommand{
		client:    client,
		serviceID: serviceID,
		force:     force,
	}
}

// Run executes the delete command.
func (c *DeleteCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	if !c.force {
		fmt.Printf("Are you sure you want to delete service '%s'? (y/N): ", c.serviceID)
		var confirm string
		fmt.Scanln(&confirm)
		if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if err := c.client.DeleteService(c.serviceID); err != nil {
		return err
	}

	fmt.Printf("Deleted service '%s'\n", c.serviceID)
	return nil
}

// LogsCommand handles the 'logs' subcommand.
type LogsCommand struct {
	client    *Client
	serviceID string
	lines     int
}

// NewLogsCommand creates a new logs command.
func NewLogsCommand(client *Client, serviceID string, lines int) *LogsCommand {
	return &LogsCommand{
		client:    client,
		serviceID: serviceID,
		lines:     lines,
	}
}

// Run executes the logs command.
func (c *LogsCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	lines := c.lines
	if lines <= 0 {
		lines = 100
	}

	logs, err := c.client.GetServiceLogs(c.serviceID, lines)
	if err != nil {
		return err
	}

	if logs == "" {
		fmt.Println("No logs available.")
		return nil
	}

	fmt.Print(logs)
	return nil
}

// EnableCommand handles the 'enable' subcommand.
type EnableCommand struct {
	client    *Client
	serviceID string
}

// NewEnableCommand creates a new enable command.
func NewEnableCommand(client *Client, serviceID string) *EnableCommand {
	return &EnableCommand{
		client:    client,
		serviceID: serviceID,
	}
}

// Run executes the enable command.
func (c *EnableCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	enabled := true
	req := &ServiceRequest{Enabled: &enabled}
	svc, err := c.client.UpdateService(c.serviceID, req)
	if err != nil {
		return err
	}

	fmt.Printf("Enabled service '%s'\n", svc.ID)
	return nil
}

// EventsCommand handles the 'events' subcommand.
type EventsCommand struct {
	client *Client
	limit  int
	json   bool
	follow bool
}

// NewEventsCommand creates a new events command.
func NewEventsCommand(client *Client, limit int, jsonOutput bool, follow bool) *EventsCommand {
	return &EventsCommand{
		client: client,
		limit:  limit,
		json:   jsonOutput,
		follow: follow,
	}
}

// Run executes the events command.
func (c *EventsCommand) Run() error {
	limit := c.limit
	if limit <= 0 {
		limit = 50
	}

	if c.follow {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle interrupt signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			cancel()
		}()

		return c.client.FollowEvents(ctx, limit, func(data string) {
			if c.json {
				fmt.Println(data)
				return
			}
			// Parse and format the event
			var e EventLogEntry
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				fmt.Println(data)
				return
			}
			msg := e.Message
			if len(msg) > 60 {
				msg = msg[:57] + "..."
			}
			fmt.Printf("%s  %-20s  %-12s  %s\n",
				e.CreatedAt.Format("2006-01-02 15:04:05"), e.ServiceName, e.EventType, msg)
		})
	}

	resp, err := c.client.GetEventLog(limit)
	if err != nil {
		return err
	}

	if c.json {
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal response: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if len(resp.Events) == 0 {
		fmt.Println("No events found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tSERVICE\tEVENT\tMESSAGE")
	for _, e := range resp.Events {
		msg := e.Message
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			e.CreatedAt.Format("2006-01-02 15:04:05"), e.ServiceName, e.EventType, msg)
	}
	w.Flush()

	fmt.Printf("\nShowing %d event(s)\n", len(resp.Events))
	return nil
}

// DisableCommand handles the 'disable' subcommand.
type DisableCommand struct {
	client    *Client
	serviceID string
}

// NewDisableCommand creates a new disable command.
func NewDisableCommand(client *Client, serviceID string) *DisableCommand {
	return &DisableCommand{
		client:    client,
		serviceID: serviceID,
	}
}

// Run executes the disable command.
func (c *DisableCommand) Run() error {
	if c.serviceID == "" {
		return fmt.Errorf("service ID is required")
	}

	enabled := false
	req := &ServiceRequest{Enabled: &enabled}
	svc, err := c.client.UpdateService(c.serviceID, req)
	if err != nil {
		return err
	}

	fmt.Printf("Disabled service '%s'\n", svc.ID)
	return nil
}
