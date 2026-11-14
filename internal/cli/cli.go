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

	// --help/-h print the command overview, not the server flag usage.
	if isHelpFlag(cmd) {
		printGeneralHelp()
		return true, nil
	}

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
	case "dump":
		return true, runDumpCommand(args[2:])
	case "start":
		return true, runActionCommand("start", args[2:])
	case "stop":
		return true, runActionCommand("stop", args[2:])
	case "restart":
		return true, runActionCommand("restart", args[2:])
	case "pull":
		return true, runPullCommand(args[2:])
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
	case "doctor":
		return true, runDoctorCommand(args[2:])
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

// isHelpFlag reports whether arg asks for help (-h, -help, --help).
func isHelpFlag(arg string) bool {
	switch arg {
	case "-h", "-help", "--help":
		return true
	}
	return false
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

func runDumpCommand(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign dump [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	client := NewClient(addr)
	cmd := NewDumpCommand(client, fs.Arg(0))
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
	var pull *bool
	if action == "restart" {
		pull = fs.Bool("pull", false, "Pull images before restarting (compose only)")
	}
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
	if pull != nil {
		cmd.pull = *pull
	}
	return cmd.Run()
}

func runPullCommand(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: reign pull [options] <service-id>")
	}

	addr := *server
	if addr == "" {
		addr = os.Getenv("REIGN_SERVER")
	}
	if addr == "" {
		addr = defaultServerAddr
	}

	return NewPullCommand(NewClient(addr), fs.Arg(0)).Run()
}

func runDoctorCommand(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
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

	return NewDoctorCommand(NewClient(addr), *jsonOutput).Run()
}

func runCreateCommand(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")

	// JSON input options
	jsonFile := fs.String("f", "", "JSON file containing service definition")
	jsonFileL := fs.String("file", "", "JSON file containing service definition (long form)")
	jsonStr := fs.String("json", "", "Inline JSON string containing service definition")

	// Individual field flags
	id := fs.String("id", "", "Service ID (required)")
	name := fs.String("name", "", "Service name (required)")
	svcType := fs.String("type", "", "Service type: 'compose' or 'binary' (required)")
	path := fs.String("path", "", "Path to compose file or binary (required)")
	command := fs.String("command", "", "Command for binary services (optional)")
	workDir := fs.String("workdir", "", "Working directory for binary services (optional)")
	envFile := fs.String("envfile", "", "Path to env file for binary services (optional)")
	pullPolicy := fs.String("pull", "", "Image pull policy for compose services: missing, always, never (default: missing)")
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

	// Check for inline JSON string, stdin, or file input
	var jsonData string
	if *jsonStr != "" {
		jsonData = *jsonStr
	} else if file == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		jsonData = string(data)
		file = ""
	}

	opts := &CreateUpdateOptions{
		JSONFile:   file,
		JSONData:   jsonData,
		ID:         *id,
		Name:       *name,
		Type:       *svcType,
		Path:       *path,
		Command:    *command,
		WorkDir:    *workDir,
		EnvFile:    *envFile,
		PullPolicy: *pullPolicy,
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
	workDir := fs.String("workdir", "", "Working directory for binary services")
	envFile := fs.String("envfile", "", "Path to env file for binary services")
	pullPolicy := fs.String("pull", "", "Image pull policy for compose services: missing, always, never")
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
		JSONFile:   file,
		JSONData:   jsonData,
		Name:       *name,
		Type:       *svcType,
		Path:       *path,
		Command:    *command,
		WorkDir:    *workDir,
		EnvFile:    *envFile,
		PullPolicy: *pullPolicy,
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
	yes := fs.Bool("y", false, "Delete without confirmation")
	yesL := fs.Bool("yes", false, "Delete without confirmation (long form)")
	force := fs.Bool("f", false, "Alias for --yes")
	forceL := fs.Bool("force", false, "Alias for --yes (long form)")
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
	cmd := NewDeleteCommand(client, fs.Arg(0), *yes || *yesL || *force || *forceL)
	return cmd.Run()
}

func runLogsCommand(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	server := fs.String("server", "", "Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)")
	lines := fs.Int("n", 100, "Number of lines to show")
	linesL := fs.Int("lines", 100, "Number of lines to show (long form)")
	follow := fs.Bool("f", false, "Follow log output")
	followL := fs.Bool("follow", false, "Follow log output (long form)")
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

	f := *follow || *followL

	client := NewClient(addr)
	cmd := NewLogsCommand(client, fs.Arg(0), l, f)
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
		case "dump":
			printDumpHelp()
		case "create", "add":
			printCreateHelp()
		case "update", "set":
			printUpdateHelp()
		case "start", "stop":
			printActionHelp(args[0])
		case "restart":
			printRestartHelp()
		case "pull":
			printPullHelp()
		case "doctor":
			printDoctorHelp()
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
  dump            Dump a service definition as compact JSON
  create, add     Create a new service
  update, set     Update an existing service
  delete, rm      Delete a service
  start           Start a service
  stop            Stop a service
  restart         Restart a service
  pull            Pull a compose service's images (without restarting)
  logs            View service logs
  events          View event log across all services
  enable          Enable a service
  disable         Disable a service
  doctor          Find compose projects and systemd units that conflict with reign
  help            Show help for a command
  version         Show version information

Global Options:
  --server        Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -h, --help      Show this help

Server Options (reign serve):
  -c, -config     Path to configuration file
  -v, -version    Show version information

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

func printDumpHelp() {
	fmt.Print(`Usage: reign dump [options] <service-id>

Dump the JSON definition of a service on a single line. The output is
suitable for defining a new service or updating an existing one.

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)

Examples:
  reign dump myservice
  reign dump myservice | reign create -f -
  reign dump myservice | reign update myservice -f -
`)
}

func printCreateHelp() {
	fmt.Print(`Usage: reign create [options]

Create a new service.

Aliases: add

Options:
  --server          Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -f, --file        JSON file containing service definition (use '-' for stdin)
  -json             Inline JSON string containing service definition

  Service fields:
  --id              Service ID (required)
  --name            Service name (required)
  --type            Service type: 'compose' or 'binary' (required)
  --path            Path to compose file (required for compose)
  --command         Command to run (required for binary)
  --workdir         Working directory for binary services
  --envfile         Env file for binary services
  --pull            Image pull policy for compose: missing, always, never (default: missing)
  --enabled         Whether service is enabled: true/false (default: true)
  --infrastructure  Whether service is infrastructure: true/false (default: false)

Note: Individual flags override values from JSON file.

Examples:
  reign create --id myservice --name "My Service" --type compose --path /opt/myservice/docker-compose.yml
  reign create --id myapp --name "My App" --type binary --command "/usr/local/bin/myapp serve --port 8080" --workdir /opt/myapp
  reign create -f service.json
  reign create -json '{"id":"myapp","name":"My App","type":"binary","command":"/usr/local/bin/myapp"}'
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
  --workdir         Working directory for binary services
  --envfile         Env file for binary services
  --pull            Image pull policy for compose: missing, always, never
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

func printRestartHelp() {
	fmt.Print(`Usage: reign restart [options] <service-id>

Restart a service. Images are not pulled unless --pull is given (or the
service's pull policy is 'always').

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  --pull      Pull images first, while the service keeps running. A failed
              pull aborts the restart. Compose services only.

Examples:
  reign restart myservice
  reign restart --pull myservice
`)
}

func printPullHelp() {
	fmt.Print(`Usage: reign pull [options] <service-id>

Pull the images of a compose service without restarting it. Use
'reign restart' afterwards to switch to the new images, or
'reign restart --pull' to do both in one step.

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)

Examples:
  reign pull myservice && reign restart myservice
`)
}

func printDoctorHelp() {
	fmt.Print(`Usage: reign doctor [options]

Check the host for things reign should know about:

  - running compose projects whose working directory is not the path of any
    reign service (from the com.docker.compose.project.working_dir label)
  - systemd units in /etc/systemd/system that run docker compose for a path
    reign manages, with whether they start at boot and how often they restarted

The check runs on the reign server. It exits non-zero when it finds problems.

Options:
  --server    Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  --json      Output in JSON format

Examples:
  reign doctor
  reign doctor --json
`)
}

func printDeleteHelp() {
	fmt.Print(`Usage: reign delete [options] <service-id>

Delete a service. If the service is running, it will be stopped first.

Without --yes, reign asks for confirmation on the terminal. When stdin is
not a terminal (scripts, pipes), --yes is required and the command fails
otherwise.

Aliases: rm, remove

Options:
  --server        Reign server address (default: $REIGN_SERVER or http://127.0.0.1:7890)
  -y, --yes       Delete without confirmation prompt (also -f, --force)

Examples:
  reign delete myservice
  reign delete -y myservice
  reign rm --yes myservice
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
