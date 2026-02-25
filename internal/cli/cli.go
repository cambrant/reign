package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/reign/internal/version"
)

const defaultServerAddr = "http://127.0.0.1:7890"

// Run parses CLI arguments and executes the appropriate command.
// Returns true if a CLI command was executed (so the server shouldn't start).
func Run(args []string) (bool, error) {
	if len(args) < 2 {
		printGeneralHelp()
		return true, nil
	}

	// Check if the first argument is a CLI command
	cmd := args[1]

	// Skip if it looks like server flags
	if strings.HasPrefix(cmd, "-") {
		return false, nil
	}

	// Special case: "serve" explicitly starts the server
	if cmd == "serve" {
		// Remove "serve" from args so the rest can be parsed normally
		os.Args = append([]string{args[0]}, args[2:]...)
		return false, nil
	}

	// Handle CLI commands
	switch cmd {
	case "list", "ls":
		return true, runListCommand(args[2:])
	case "status", "ps":
		return true, runListCommand(args[2:])
	case "show", "get":
		return true, runShowCommand(args[2:])
	case "start":
		return true, runActionCommand("start", args[2:])
	case "stop":
		return true, runActionCommand("stop", args[2:])
	case "restart":
		return true, runActionCommand("restart", args[2:])
	case "create", "add":
		return true, runCreateCommand(args[2:])
	case "update", "set":
		return true, runUpdateCommand(args[2:])
	case "delete", "rm", "remove":
		return true, runDeleteCommand(args[2:])
	case "logs":
		return true, runLogsCommand(args[2:])
	case "events":
		return true, runEventsCommand(args[2:])
	case "enable":
		return true, runEnableCommand(args[2:])
	case "disable":
		return true, runDisableCommand(args[2:])
	case "help":
		return true, runHelpCommand(args[2:])
	case "version":
		fmt.Println(version.Get())
		return true, nil
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printGeneralHelp()
		os.Exit(1)
		return true, nil
	}
}

func getServerAddr(fs *flag.FlagSet) string {
	addr := fs.Lookup("server")
	if addr != nil && addr.Value.String() != "" {
		return addr.Value.String()
	}
	if envAddr := os.Getenv("REIGN_SERVER"); envAddr != "" {
		return envAddr
	}
	return defaultServerAddr
}

func runListCommand(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	fs.Parse(args)

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewListCommand(client, *jsonOutput)
	return cmd.Run()
}

func runShowCommand(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	jsonOutput := fs.Bool("json", false, "Output service definition as JSON (suitable for create/update)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign show [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewShowCommand(client, fs.Arg(0), *jsonOutput)
	return cmd.Run()
}

func runActionCommand(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign %s [options] <service-id>", action)
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewActionCommand(client, action, fs.Arg(0))
	return cmd.Run()
}

func runCreateCommand(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")

	// JSON input options
	jsonFile := fs.String("f", "", "JSON file containing service definition")
	jsonFileL := fs.String("file", "", "JSON file containing service definition (long form)")

	// Individual field flags
	id := fs.String("id", "", "Service ID (required)")
	name := fs.String("name", "", "Service name (required)")
	svcType := fs.String("type", "", "Service type: 'compose' or 'binary' (required)")
	path := fs.String("path", "", "Path to compose file or binary (required)")
	command := fs.String("command", "", "Command for binary services (optional)")
	enabled := fs.String("enabled", "", "Whether service is enabled: true/false (default: true)")
	infrastructure := fs.String("infrastructure", "", "Whether service is infrastructure: true/false (default: false)")

	fs.Parse(args)

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	// Determine JSON file
	file := *jsonFile
	if file == "" {
		file = *jsonFileL
	}

	// Check for stdin input
	var jsonData string
	if file == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		jsonData = string(data)
		file = ""
	}

	opts := &CreateUpdateOptions{
		JSONFile: file,
		JSONData: jsonData,
		ID:       *id,
		Name:     *name,
		Type:     *svcType,
		Path:     *path,
		Command:  *command,
	}

	if *enabled != "" {
		val := strings.ToLower(*enabled) == "true" || *enabled == "1"
		opts.Enabled = &val
	}
	if *infrastructure != "" {
		val := strings.ToLower(*infrastructure) == "true" || *infrastructure == "1"
		opts.Infrastructure = &val
	}

	client := NewClient(addr)
	cmd := NewCreateCommand(client, opts)
	return cmd.Run()
}

func runUpdateCommand(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")

	// JSON input options
	jsonFile := fs.String("f", "", "JSON file containing service definition")
	jsonFileL := fs.String("file", "", "JSON file containing service definition (long form)")

	// Individual field flags
	name := fs.String("name", "", "Service name")
	svcType := fs.String("type", "", "Service type: 'compose' or 'binary'")
	path := fs.String("path", "", "Path to compose file or binary")
	command := fs.String("command", "", "Command for binary services")
	enabled := fs.String("enabled", "", "Whether service is enabled: true/false")
	infrastructure := fs.String("infrastructure", "", "Whether service is infrastructure: true/false")

	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign update [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	// Determine JSON file
	file := *jsonFile
	if file == "" {
		file = *jsonFileL
	}

	// Check for stdin input
	var jsonData string
	if file == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		jsonData = string(data)
		file = ""
	}

	opts := &CreateUpdateOptions{
		JSONFile: file,
		JSONData: jsonData,
		Name:     *name,
		Type:     *svcType,
		Path:     *path,
		Command:  *command,
	}

	if *enabled != "" {
		val := strings.ToLower(*enabled) == "true" || *enabled == "1"
		opts.Enabled = &val
	}
	if *infrastructure != "" {
		val := strings.ToLower(*infrastructure) == "true" || *infrastructure == "1"
		opts.Infrastructure = &val
	}

	client := NewClient(addr)
	cmd := NewUpdateCommand(client, fs.Arg(0), opts)
	return cmd.Run()
}

func runDeleteCommand(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	force := fs.Bool("f", false, "Force deletion without confirmation")
	forceL := fs.Bool("force", false, "Force deletion without confirmation (long form)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign delete [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewDeleteCommand(client, fs.Arg(0), *force || *forceL)
	return cmd.Run()
}

func runLogsCommand(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	lines := fs.Int("n", 100, "Number of lines to show")
	linesL := fs.Int("lines", 100, "Number of lines to show (long form)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign logs [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	l := *lines
	if *linesL != 100 {
		l = *linesL
	}

	client := NewClient(addr)
	cmd := NewLogsCommand(client, fs.Arg(0), l)
	return cmd.Run()
}

func runEnableCommand(args []string) error {
	fs := flag.NewFlagSet("enable", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign enable [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewEnableCommand(client, fs.Arg(0))
	return cmd.Run()
}

func runEventsCommand(args []string) error {
	fs := flag.NewFlagSet("events", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	limit := fs.Int("n", 50, "Number of events to show")
	limitL := fs.Int("limit", 50, "Number of events to show (long form)")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	follow := fs.Bool("f", false, "Follow events in real time")
	followL := fs.Bool("follow", false, "Follow events in real time (long form)")
	fs.Parse(args)

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	l := *limit
	if *limitL != 50 {
		l = *limitL
	}

	f := *follow || *followL

	client := NewClient(addr)
	cmd := NewEventsCommand(client, l, *jsonOutput, f)
	return cmd.Run()
}

func runDisableCommand(args []string) error {
	fs := flag.NewFlagSet("disable", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign disable [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewDisableCommand(client, fs.Arg(0))
	return cmd.Run()
}

func runHelpCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list", "ls":
			printListHelp()
		case "show", "get":
			printShowHelp()
		case "create", "add":
			printCreateHelp()
		case "update", "set":
			printUpdateHelp()
		case "start", "stop", "restart":
			printActionHelp(args[0])
		case "delete", "rm", "remove":
			printDeleteHelp()
		case "logs":
			printLogsHelp()
		case "events":
			printEventsHelp()
		case "enable", "disable":
			printEnableDisableHelp(args[0])
		default:
			printGeneralHelp()
		}
		return nil
	}
	printGeneralHelp()
	return nil
}

func printGeneralHelp() {
	fmt.Printf(`Reign - Service Orchestrator

Usage:
  reign <command> [options] [arguments]
  reign serve [server-options]         Start the Reign server

Commands:
  list, ls        List all services and their status
  show, get       Show detailed information about a service
  create, add     Create a new service
  update, set     Update an existing service
  delete, rm      Delete a service
  start           Start a service
  stop            Stop a service
  restart         Restart a service
  logs            View service logs
  events          View event log across all services
  enable          Enable a service
  disable         Disable a service
  help            Show help for a command
  version         Show version information

Global Options:
  --server        Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)

Environment Variables:
  REIGN_SERVER    Default server address

Run 'reign help <command>' for more information on a specific command.
`)
}

func printListHelp() {
	fmt.Print(`Usage: reign list [options]

List all services and their current status.

Aliases: ls, status, ps

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  --json      Output in JSON format

Examples:
  reign list
  reign list --json
  reign ls
`)
}

func printShowHelp() {
	fmt.Print(`Usage: reign show [options] <service-id>

Show detailed information about a service, including its configuration,
current state, and recent events.

Aliases: get

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  --json      Output service definition as JSON (suitable for create/update)

Examples:
  reign show myservice
  reign show --json myservice > service.json
  reign show myservice --json | reign update myservice -f -
`)
}

func printCreateHelp() {
	fmt.Print(`Usage: reign create [options]

Create a new service.

Aliases: add

Options:
  --server          Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -f, --file        JSON file containing service definition (use '-' for stdin)

  Service fields:
  --id              Service ID (required)
  --name            Service name (required)
  --type            Service type: 'compose' or 'binary' (required)
  --path            Path to compose file or binary (required)
  --command         Command for binary services
  --enabled         Whether service is enabled: true/false (default: true)
  --infrastructure  Whether service is infrastructure: true/false (default: false)

Note: Individual flags override values from JSON file.

Examples:
  reign create --id myservice --name "My Service" --type compose --path /opt/myservice/docker-compose.yml
  reign create --id myapp --name "My App" --type binary --path /usr/local/bin/myapp --command "serve --port 8080"
  reign create -f service.json
  cat service.json | reign create -f -
  reign show --json oldservice | reign create --id newservice -f -
`)
}

func printUpdateHelp() {
	fmt.Print(`Usage: reign update [options] <service-id>

Update an existing service configuration.

Aliases: set

Options:
  --server          Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -f, --file        JSON file containing service definition (use '-' for stdin)

  Service fields (only provided fields are updated):
  --name            Service name
  --type            Service type: 'compose' or 'binary'
  --path            Path to compose file or binary
  --command         Command for binary services
  --enabled         Whether service is enabled: true/false
  --infrastructure  Whether service is infrastructure: true/false

Note: Individual flags override values from JSON file.

Examples:
  reign update myservice --name "New Name"
  reign update myservice --enabled false
  reign update myservice -f updated-config.json
  reign show --json myservice | jq '.name = "New Name"' | reign update myservice -f -
`)
}

func printActionHelp(action string) {
	fmt.Printf(`Usage: reign %s [options] <service-id>

%s a service.

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)

Examples:
  reign %s myservice
`, action, strings.Title(action), action)
}

func printDeleteHelp() {
	fmt.Print(`Usage: reign delete [options] <service-id>

Delete a service. If the service is running, it will be stopped first.

Aliases: rm, remove

Options:
  --server        Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -f, --force     Delete without confirmation prompt

Examples:
  reign delete myservice
  reign delete -f myservice
  reign rm myservice
`)
}

func printLogsHelp() {
	fmt.Print(`Usage: reign logs [options] <service-id>

View logs for a service.

Options:
  --server        Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -n, --lines     Number of lines to show (default: 100)

Examples:
  reign logs myservice
  reign logs -n 50 myservice
  reign logs --lines 200 myservice
`)
}

func printEventsHelp() {
	fmt.Print(`Usage: reign events [options]

View the unified event log across all services, ordered by time.
Shows service state changes, starts, stops, failures, and other lifecycle events.

Options:
  --server        Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -n, --limit     Number of events to show (default: 50)
  --json          Output in JSON format

Examples:
  reign events
  reign events -n 100
  reign events --json
`)
}

func printEnableDisableHelp(action string) {
	opposite := "disable"
	if action == "disable" {
		opposite = "enable"
	}
	fmt.Printf(`Usage: reign %s [options] <service-id>

%s a service. When a service is disabled, it will not be started
automatically and cannot be started manually until enabled again.

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)

Examples:
  reign %s myservice
  reign %s myservice && reign start myservice
`, action, strings.Title(action), action, opposite)
}
