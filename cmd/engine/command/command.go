package command

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/platform/config"
)

// Flags holds parsed CLI flags, both join-mode bootstrap and runtime overrides.
// "Explicit" records which flags were actually present on the command line
// (a passed-but-empty value counts), which is what start-mode detection must
// use — value inference (`ConfigFile != ""`) is not reliable.
type Flags struct {
	ConfigFile string // --config: config-file mode
	Servers    string // --servers: cluster server host list (join mode)
	Port       int    // --port: cluster gRPC port (join mode only)
	DataDir    string // --data-dir: certificate base directory (join mode only)
	LogDir     string // --log-dir: audit/security log directory override (both modes)
	LogLevel   string // --log-level: application log level override (both modes)

	explicit map[string]bool
}

// Explicit reports whether the named flag was present on the command line.
func (f *Flags) Explicit(name string) bool {
	return f.explicit[name]
}

// StartMode is the resolved startup mode of the persistent engine.
type StartMode int

const (
	// StartModeConfig runs from a config.toml (server or config-file agent).
	StartModeConfig StartMode = iota
	// StartModeConfigless assembles a pure agent config in memory (join mode).
	StartModeConfigless
)

type command struct {
	name    string
	summary string
	exec    func(flags *Flags, args []string) error
}

// ErrHelpRequested signals an explicit --help/-h: usage has already been printed by
// the FlagSet and the process must terminate successfully (exit 0), symmetric with
// --version. Without it the flag package's flag.ErrHelp would be reported as a
// failure, so `design-cloud-engine --help` exited 1.
var ErrHelpRequested = errors.New("help requested")

// ParseFlags parses the top-level CLI flags and returns any remaining args.
func ParseFlags(args []string, errOut io.Writer, programName string) (*Flags, []string, error) {
	f := &Flags{explicit: make(map[string]bool), Port: config.DefaultClusterPort}
	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(errOut)
	bindStartFlags(fs, f)
	showVersion := fs.Bool("version", false, "show version only")
	fs.Usage = func() {
		printUsage(programName, fs)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, ErrHelpRequested
		}
		return nil, nil, err
	}
	fs.Visit(func(fl *flag.Flag) {
		f.explicit[fl.Name] = true
	})

	if *showVersion {
		console.PrintStdout("%s version %s\n", config.AppName, config.VERSION)
		os.Exit(0)
	}

	return f, fs.Args(), nil
}

func bindStartFlags(fs *flag.FlagSet, f *Flags) {
	fs.StringVar(&f.ConfigFile, "config", "", "config file path (default \"config.toml\")")
	fs.StringVar(&f.Servers, "servers", "", "cluster server host list, comma separated (join mode), e.g. mgt01,mgt02")
	fs.IntVar(&f.Port, "port", config.DefaultClusterPort, "cluster gRPC port (join mode only)")
	fs.StringVar(&f.DataDir, "data-dir", "", "certificate base directory (join mode only)")
	fs.StringVar(&f.LogDir, "log-dir", "", "audit/security log directory (overrides log.dir; both modes)")
	fs.StringVar(&f.LogLevel, "log-level", "", "application log level: DEBUG/INFO/WARN/ERROR (overrides log.app_level; both modes)")
}

// ResolveStartMode decides how the persistent engine is started:
//
//	conflict: --config and --servers both explicit → hard error
//	join-only flags without --servers → hard error
//	1. only --config        → config-file mode
//	2. only --servers       → join / configless mode
//	3. neither, config.toml exists → config-file mode
//	4. neither, no config.toml  → error with two-surface usage
func ResolveStartMode(f *Flags) (StartMode, error) {
	hasConfig := f.Explicit("config")
	hasServers := f.Explicit("servers")
	hasJoinOnly := f.Explicit("port") || f.Explicit("data-dir")

	if hasConfig && hasServers {
		return StartModeConfig, fmt.Errorf("cannot specify both --config and --servers: choose one")
	}
	if hasJoinOnly && !hasServers {
		return StartModeConfig, fmt.Errorf("--port and --data-dir are only valid with --servers")
	}

	switch {
	case hasConfig:
		return StartModeConfig, nil
	case hasServers:
		return StartModeConfigless, nil
	}

	if _, err := os.Stat(config.DefaultConfigFile); err == nil {
		return StartModeConfig, nil
	}

	return StartModeConfig, fmt.Errorf(
		"no configuration specified and default %q not found;\n"+
			"use one of:\n"+
			"  %s [--config <path>]                              (config-file mode)\n"+
			"  %s --servers <hosts> [--port] [--data-dir <dir>]  (configless agent)",
		config.DefaultConfigFile, config.AppName, config.AppName)
}

// StartArgv builds the argv fragment that re-launches the engine with the same
// start surface (used by Windows service install).
func StartArgv(f *Flags, mode StartMode) ([]string, error) {
	var out []string
	switch mode {
	case StartModeConfigless:
		if f.Port < 1024 || f.Port > 65535 {
			return nil, fmt.Errorf("cluster port must be in range 1024..65535; got %d", f.Port)
		}
		out = append(out, "--servers", f.Servers, "--port", strconv.Itoa(f.Port))
		if f.DataDir != "" {
			out = append(out, "--data-dir", f.DataDir)
		}
	case StartModeConfig:
		cfgPath := cmp.Or(f.ConfigFile, config.DefaultConfigFile)
		absCfg, err := filepath.Abs(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("resolve config path: %w", err)
		}
		out = append(out, "--config", absCfg)
	default:
		return nil, fmt.Errorf("unknown start mode: %d", mode)
	}
	if f.LogDir != "" {
		out = append(out, "--log-dir", f.LogDir)
	}
	if f.LogLevel != "" {
		out = append(out, "--log-level", f.LogLevel)
	}
	return out, nil
}

// MergeFlags overlays argv onto a copy of parent.
// Used by Windows service install (service_windows.go).
func MergeFlags(parent *Flags, args []string, errOut io.Writer, name string) (*Flags, error) {
	f := &Flags{
		ConfigFile: parent.ConfigFile,
		Servers:    parent.Servers,
		Port:       parent.Port,
		DataDir:    parent.DataDir,
		LogDir:     parent.LogDir,
		LogLevel:   parent.LogLevel,
		explicit:   maps.Clone(parent.explicit),
	}
	if f.explicit == nil {
		f.explicit = make(map[string]bool)
	}
	if f.Port == 0 {
		f.Port = config.DefaultClusterPort
	}

	fs := newCommandFlagSet(name, errOut)
	bindStartFlags(fs, f)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	fs.Visit(func(fl *flag.Flag) {
		f.explicit[fl.Name] = true
	})
	return f, nil
}

func newCommandFlagSet(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	return fs
}

var registeredCommands = []command{
	{name: "gen-config", summary: "Generate config.example.toml", exec: runGenConfigCommand},
	{name: "check-config", summary: "Validate a config file (parse + resolve + validate)", exec: runCheckConfigCommand},
	{name: "node", summary: "Manage cluster nodes (register, issue-token, issue-cert)", exec: runNodeCommand},
	{name: "service", summary: "Manage the local engine process or service", exec: runServiceCommand},
	{name: "cert", summary: "Manage TLS certificates (gen-portal)", exec: runCertCommand},
	{name: "desktop", summary: "Desktop client release helpers (plugin publish)", exec: runDesktopCommand},
}

// Execute runs the named subcommand.
func Execute(flags *Flags, args []string) error {
	if len(args) == 0 {
		printUsage(config.AppName, nil)
		return fmt.Errorf("missing command")
	}

	subCmd := args[0]
	for _, cmd := range registeredCommands {
		if cmd.name == subCmd {
			return cmd.exec(flags, args[1:])
		}
	}

	printUsage(config.AppName, nil)
	return fmt.Errorf("unknown command: %s", subCmd)
}

func runNodeCommand(flags *Flags, args []string) error {
	if len(args) == 0 {
		printNodeUsage()
		return fmt.Errorf("node sub-command required: register, issue-token, issue-cert")
	}
	switch args[0] {
	case "register":
		return runRegisterCommand(args[1:])
	case "issue-token":
		return runIssueTokenCommand(flags, args[1:])
	case "issue-cert":
		return runIssueCertCommand(flags, args[1:])
	default:
		printNodeUsage()
		return fmt.Errorf("unknown node command: %s", args[0])
	}
}

func printNodeUsage() {
	console.PrintStderr("Usage: %s node <register|issue-token|issue-cert> [options]\n\n", config.AppName)
	console.PrintStderr("Subcommands:\n")
	console.PrintStderr("  register                 Bootstrap node certificate materials via OTP token\n")
	console.PrintStderr("  issue-token              Issue or rotate a bootstrap token for a cluster node\n")
	console.PrintStderr("  issue-cert               Offline issue cluster certificate materials for a node\n")
}

func printUsage(programName string, fs *flag.FlagSet) {
	console.PrintStderr("+++ %s +++\n", config.AppName)
	console.PrintStderr("Version: %s\n", config.VERSION)
	console.PrintStderr("\nStart (mutually exclusive surfaces):\n")
	console.PrintStderr("  %s [--config PATH] [--log-dir DIR] [--log-level LEVEL]\n", programName)
	console.PrintStderr("      config-file mode — role from cluster.servers in the file\n")
	console.PrintStderr("  %s --servers HOSTS [--port N] [--data-dir DIR] [--log-dir DIR] [--log-level LEVEL]\n", programName)
	console.PrintStderr("      join mode — configless agent; always Agent; no config.toml\n")
	console.PrintStderr("\nCommands:\n")
	for _, cmd := range registeredCommands {
		console.PrintStderr("  %-24s %s\n", cmd.name, cmd.summary)
	}
	console.PrintlnStderr("\nOptions:")
	if fs != nil {
		fs.PrintDefaults()
	}
}
