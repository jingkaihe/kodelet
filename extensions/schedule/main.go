// kodelet-extension-schedule runs agent instructions on recurring schedules. Without
// arguments it serves Kodelet's extension protocol with tools to manage the
// schedules of the current workspace; the daemon subcommand runs them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/pkg/errors"
)

var version = "dev"

const usage = `Usage:
  kodelet-extension-schedule           Serve the Kodelet extension protocol (started by Kodelet)
  kodelet-extension-schedule daemon    Run due schedules until interrupted
  kodelet-extension-schedule list      List schedules in every workspace
  kodelet-extension-schedule version   Print the version

Run "kodelet-extension-schedule <command> -h" for command flags.
`

func main() {
	if len(os.Args) < 2 {
		os.Exit(runExtension())
	}
	switch os.Args[1] {
	case "daemon":
		os.Exit(runDaemonCommand(os.Args[2:]))
	case "list":
		os.Exit(runListCommand(os.Args[2:], os.Stdout))
	case "version", "--version":
		fmt.Println("kodelet-extension-schedule", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func runExtension() int {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	path, err := defaultDBPath()
	if err != nil {
		slog.Error("schedule extension failed", "error", err)
		return 1
	}
	stores := &storeOpener{path: path}
	defer stores.Close()
	ext := extension.New(extension.Metadata{Name: "schedule", Version: version})
	registerTools(ext, stores, time.Now)
	if err := ext.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("schedule extension failed", "error", err)
		return 1
	}
	return 0
}

func runDaemonCommand(args []string) int {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	config := defaultDaemonConfig()
	dbPath := flags.String("db", "", "schedule database (default $KODELET_SCHEDULE_DB or ~/.kodelet/schedule/schedule.db)")
	server := flags.String("server", "", "Kodelet server URL (default: the kodelet CLI's configured or local server)")
	command := flags.String("kodelet", "kodelet", "kodelet executable that runs scheduled instructions")
	flags.DurationVar(&config.PollInterval, "poll-interval", config.PollInterval, "how often to check for due schedules")
	flags.IntVar(&config.Concurrency, "concurrency", config.Concurrency, "maximum number of runs at once")
	flags.DurationVar(&config.RunTimeout, "run-timeout", config.RunTimeout, "cancel runs that take longer than this (0 disables)")
	flags.DurationVar(&config.MisfireGrace, "misfire-grace", config.MisfireGrace, "skip runs missed by more than this, for example while the host slept")
	flags.DurationVar(&config.ShutdownGrace, "shutdown-grace", config.ShutdownGrace, "on shutdown, wait this long for runs to finish before cancelling them")
	if err := flags.Parse(args); err != nil {
		return flagExitCode(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := config.validate(); err != nil {
		logger.Error("invalid daemon configuration", "error", err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runDaemon(ctx, *dbPath, *server, *command, config, logger); err != nil {
		logger.Error("schedule daemon failed", "error", err)
		return 1
	}
	return 0
}

func runDaemon(ctx context.Context, dbPath, server, command string, config daemonConfig, logger *slog.Logger) error {
	command, err := exec.LookPath(command)
	if err != nil {
		return errors.Wrap(err, "kodelet executable not found; pass --kodelet")
	}
	dbPath, err = resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	lock, err := acquireDaemonLock(dbPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	logger.Info("using schedule database", "path", dbPath, "kodelet", command)
	d := newDaemon(store, agentExecutor{server: server, command: command}, config, logger)
	return d.run(ctx)
}

func runListCommand(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	dbPath := flags.String("db", "", "schedule database (default $KODELET_SCHEDULE_DB or ~/.kodelet/schedule/schedule.db)")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if err := listSchedules(context.Background(), *dbPath, *jsonOutput, output); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func listSchedules(ctx context.Context, dbPath string, jsonOutput bool, output io.Writer) error {
	dbPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	schedules, err := store.List(ctx, "")
	if err != nil {
		return err
	}
	if schedules == nil {
		schedules = []Schedule{}
	}
	status, found, err := store.Daemon(ctx)
	if err != nil {
		return err
	}
	health := assessDaemon(status, found, time.Now())
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{"schedules": schedules, "daemon": health})
	}
	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tWorkspace\tName\tSchedule\tEnabled\tNext run\tLast run\tDirectory")
	for _, schedule := range schedules {
		next := "-"
		if schedule.Enabled && schedule.NextRunAt != nil {
			next = formatTime(*schedule.NextRunAt)
		}
		last := "-"
		if schedule.LastRun != nil {
			last = string(schedule.LastRun.Status) + " " + formatTime(schedule.LastRun.ScheduledFor)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			schedule.ID, schedule.Workspace, schedule.Name, schedule.Schedule,
			yesNo(schedule.Enabled), next, last, schedule.CWD,
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(output, "\nDaemon: %s\n", health.State)
	if health.Warning != "" {
		fmt.Fprintf(output, "Warning: %s\n", health.Warning)
	}
	return nil
}

func resolveDBPath(path string) (string, error) {
	if path == "" {
		return defaultDBPath()
	}
	return filepath.Abs(path)
}

func flagExitCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}
