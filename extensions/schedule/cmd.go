package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/pkg/errors"
	"github.com/spf13/cast"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Settings keys. Each can be set by a flag, a KODELET_SCHEDULE_<KEY>
// environment variable, or the config file, in that order of precedence.
const (
	keyConfig       = "config"
	keyDB           = "db"
	keyAutoApprove  = "auto_approve"
	keyServer       = "server"
	keyKodelet      = "kodelet"
	keyPollInterval = "poll_interval"
	keyConcurrency  = "concurrency"
	keyRunTimeout   = "run_timeout"
	keyMisfireGrace = "misfire_grace"
)

const rootLong = `Run agent instructions on recurring schedules.

Without a command, serve the Kodelet extension protocol on stdin and stdout.
Kodelet starts the executable this way to give agents tools that manage the
schedules of their workspace. Run "daemon" separately to start due runs.

Settings come from flags, KODELET_SCHEDULE_* environment variables (for
example KODELET_SCHEDULE_DB or KODELET_SCHEDULE_POLL_INTERVAL), and the config
file, in that order of precedence. The extension creates, changes, and
deletes schedules without asking the user; set auto_approve to false to
require approval.`

// cli holds the settings shared by commands and the entry points they run,
// which tests replace.
type cli struct {
	v            *viper.Viper
	runExtension func(ctx context.Context, settings extensionSettings) error
	runDaemon    func(ctx context.Context, settings daemonSettings, logger *slog.Logger) error
}

func newCLI() *cli {
	v := viper.New()
	v.SetEnvPrefix("KODELET_SCHEDULE")
	v.AutomaticEnv()
	// An agent with a shell can already schedule work with cron or systemd;
	// these tools keep that work visible and bounded, so they do not ask by
	// default.
	v.SetDefault(keyAutoApprove, true)
	return &cli{v: v, runExtension: runExtension, runDaemon: runDaemon}
}

func newRootCommand() *cobra.Command {
	return newCLI().rootCommand()
}

func (c *cli) rootCommand() *cobra.Command {
	v := c.v
	root := &cobra.Command{
		Use:          "kodelet-extension-schedule",
		Short:        "Run agent instructions on recurring schedules",
		Long:         rootLong,
		Args:         cobra.NoArgs,
		Version:      version,
		SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return loadConfigFile(v)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := extensionSettingsFrom(v)
			if err != nil {
				return err
			}
			return c.runExtension(cmd.Context(), settings)
		},
	}
	root.SetVersionTemplate("kodelet-extension-schedule {{.Version}}\n")
	flags := root.PersistentFlags()
	flags.String("config", "", "config file (default <base>/schedule/config.yaml when present)")
	flags.String("db", "", "schedule database (default <base>/schedule/schedule.db; relative paths are under <base>/schedule)")
	mustBind(v, keyConfig, flags.Lookup("config"))
	mustBind(v, keyDB, flags.Lookup("db"))

	root.AddCommand(c.daemonCommand(), c.listCommand(), versionCommand())
	return root
}

func (c *cli) daemonCommand() *cobra.Command {
	v := c.v
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start due scheduled runs until interrupted",
		Long: `Start due scheduled runs until interrupted.

Each run starts a new conversation through "kodelet acp" on the schedule's
runner, directory, and model profile. Runs keep going on the server when the
daemon stops, and the daemon records their outcomes when it starts again.
Supervise the daemon with launchd or systemd; only one daemon can use a
database at a time.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := daemonSettingsFrom(v)
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			return c.runDaemon(cmd.Context(), settings, logger)
		},
	}
	defaults := defaultDaemonConfig()
	flags := cmd.Flags()
	flags.String("server", "", "Kodelet server URL (default: the kodelet CLI's configured or local server)")
	flags.String("kodelet", "kodelet", "kodelet executable that runs scheduled instructions")
	flags.Duration("poll-interval", defaults.PollInterval, "how often to check for due schedules")
	flags.Int("concurrency", defaults.Concurrency, "maximum number of runs at once")
	flags.Duration("run-timeout", defaults.RunTimeout, "stop runs that take longer than this (0 disables)")
	flags.Duration("misfire-grace", defaults.MisfireGrace, "skip runs missed by more than this, for example while the host slept")
	mustBind(v, keyServer, flags.Lookup("server"))
	mustBind(v, keyKodelet, flags.Lookup("kodelet"))
	mustBind(v, keyPollInterval, flags.Lookup("poll-interval"))
	mustBind(v, keyConcurrency, flags.Lookup("concurrency"))
	mustBind(v, keyRunTimeout, flags.Lookup("run-timeout"))
	mustBind(v, keyMisfireGrace, flags.Lookup("misfire-grace"))
	return cmd
}

func (c *cli) listCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List schedules of every workspace on this host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := dbPathFrom(c.v)
			if err != nil {
				return err
			}
			return listSchedules(cmd.Context(), path, jsonOutput, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON")
	return cmd
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "kodelet-extension-schedule", version)
		},
	}
}

func mustBind(v *viper.Viper, key string, flag *pflag.Flag) {
	if err := v.BindPFlag(key, flag); err != nil {
		panic(err)
	}
}

// basePath returns KODELET_BASE_PATH, or ~/.kodelet.
func basePath() (string, error) {
	if base := os.Getenv("KODELET_BASE_PATH"); base != "" {
		return filepath.Abs(base)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve home directory")
	}
	return filepath.Join(home, ".kodelet"), nil
}

// loadConfigFile reads the explicit config file, which must exist, or the
// default one when present.
func loadConfigFile(v *viper.Viper) error {
	path := strings.TrimSpace(v.GetString(keyConfig))
	explicit := path != ""
	if !explicit {
		base, err := basePath()
		if err != nil {
			return err
		}
		path = filepath.Join(base, "schedule", "config.yaml")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return errors.Wrapf(err, "failed to read config file %s", path)
	}
	return nil
}

// dbPathFrom returns the configured database, or schedule/schedule.db under
// the Kodelet base path. It expands a leading ~ and resolves a relative path
// under <base>/schedule, never the working directory, which differs between
// the extension (started in its executable's directory) and the daemon.
func dbPathFrom(v *viper.Viper) (string, error) {
	base, err := basePath()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "schedule")
	path := strings.TrimSpace(v.GetString(keyDB))
	if path == "" {
		return filepath.Join(dir, "schedule.db"), nil
	}
	if path, err = expandHome(path); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), nil
}

// expandHome replaces a leading ~ or ~/ with the home directory.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve home directory")
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

// extensionSettings are the resolved options of the extension mode.
type extensionSettings struct {
	DBPath string
	// AutoApprove saves schedule changes without asking the user; it is on by
	// default. It is read only from this executable's settings, never from
	// Kodelet's extensions.config, so a repository's kodelet-config.yaml
	// cannot turn approval off.
	AutoApprove bool
}

func extensionSettingsFrom(v *viper.Viper) (extensionSettings, error) {
	var settings extensionSettings
	var err error
	if settings.DBPath, err = dbPathFrom(v); err != nil {
		return settings, err
	}
	if settings.AutoApprove, err = cast.ToBoolE(v.Get(keyAutoApprove)); err != nil {
		return settings, errors.Errorf("invalid %s: %v", keyAutoApprove, v.Get(keyAutoApprove))
	}
	return settings, nil
}

// daemonSettings are the resolved daemon options.
type daemonSettings struct {
	DBPath  string
	Server  string
	Kodelet string
	Config  daemonConfig
}

func daemonSettingsFrom(v *viper.Viper) (daemonSettings, error) {
	var settings daemonSettings
	var err error
	if settings.DBPath, err = dbPathFrom(v); err != nil {
		return settings, err
	}
	settings.Server = strings.TrimSpace(v.GetString(keyServer))
	settings.Kodelet = strings.TrimSpace(v.GetString(keyKodelet))
	durations := []struct {
		key    string
		target *time.Duration
	}{
		{keyPollInterval, &settings.Config.PollInterval},
		{keyRunTimeout, &settings.Config.RunTimeout},
		{keyMisfireGrace, &settings.Config.MisfireGrace},
	}
	for _, d := range durations {
		if *d.target, err = cast.ToDurationE(v.Get(d.key)); err != nil {
			return settings, errors.Errorf("invalid %s: %v", d.key, v.Get(d.key))
		}
	}
	if settings.Config.Concurrency, err = cast.ToIntE(v.Get(keyConcurrency)); err != nil {
		return settings, errors.Errorf("invalid %s: %v", keyConcurrency, v.Get(keyConcurrency))
	}
	if err := settings.Config.validate(); err != nil {
		return settings, err
	}
	return settings, nil
}

// runExtension serves the extension protocol on stdin and stdout. The SDK
// redirects os.Stdout to stderr while serving, so stray output cannot corrupt
// the protocol stream.
func runExtension(ctx context.Context, settings extensionSettings) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	stores := &storeOpener{path: settings.DBPath}
	defer stores.Close()
	ext := extension.New(extension.Metadata{Name: "schedule", Version: version})
	configureExtension(ext, stores, time.Now, settings.AutoApprove)
	if err := ext.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runDaemon(ctx context.Context, settings daemonSettings, logger *slog.Logger) error {
	command, err := exec.LookPath(settings.Kodelet)
	if err != nil {
		return errors.Wrapf(err, "kodelet executable %q not found; set --kodelet", settings.Kodelet)
	}
	store, err := openStore(ctx, settings.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	lock, err := acquireDaemonLock(settings.DBPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	logger.Info("using schedule database", "path", settings.DBPath, "kodelet", command)
	runs := agentExecutor{server: settings.Server, command: command}
	d := newDaemon(store, runs, runs, settings.Config, logger)
	return d.run(ctx)
}

func listSchedules(ctx context.Context, dbPath string, jsonOutput bool, output io.Writer) error {
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
