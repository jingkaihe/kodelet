package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/presenter"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const defaultRunnerServer = "http://localhost:8080"

type runnerStartConfig struct {
	Server      string
	AuthToken   string
	DisplayName string
}

type runnerQueryConfig struct {
	Server     string
	AuthToken  string
	JSONOutput bool
}

type runnerListAPIResponse struct {
	Runners []runnerregistry.Runner `json:"runners"`
}

type runnerInspectOutput struct {
	Runner runnerregistry.Runner `json:"runner"`
	Local  *runnerLocalOutput    `json:"local,omitempty"`
}

type runnerLocalOutput struct {
	LockPath string                   `json:"lockPath"`
	Metadata *localstate.LockMetadata `json:"metadata,omitempty"`
}

var runnerCmd = &cobra.Command{
	Use:   "runner",
	Short: "Manage workspace-bound runners",
	Long:  "Start and inspect workspace-bound runner processes connected to a Kodelet control plane.",
}

var runnerStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a runner bound to the current workspace",
	RunE: func(cmd *cobra.Command, _ []string) error {
		server, _ := cmd.Flags().GetString("server")
		authToken, _ := cmd.Flags().GetString("auth-token")
		displayName, _ := cmd.Flags().GetString("name")
		return runRunnerStart(cmd.Context(), runnerStartConfig{
			Server:      server,
			AuthToken:   authToken,
			DisplayName: displayName,
		})
	},
}

var runnerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List runners registered with a control plane",
	RunE: func(cmd *cobra.Command, _ []string) error {
		config := runnerQueryConfigFromFlags(cmd)
		return runRunnerList(cmd.Context(), config, os.Stdout)
	},
}

var runnerInspectCmd = &cobra.Command{
	Use:   "inspect <runner>",
	Short: "Inspect a runner by ID, ID prefix, or display name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		config := runnerQueryConfigFromFlags(cmd)
		return runRunnerInspect(cmd.Context(), args[0], config, os.Stdout)
	},
}

func init() {
	runnerStartCmd.Flags().String("server", defaultRunnerServer, "Control-plane URL")
	runnerStartCmd.Flags().String("auth-token", os.Getenv("KODELET_RUNNER_AUTH_TOKEN"), "Runner-only authentication token (or KODELET_RUNNER_AUTH_TOKEN)")
	runnerStartCmd.Flags().String("name", "", "Optional mutable display name")
	for _, command := range []*cobra.Command{runnerListCmd, runnerInspectCmd} {
		command.Flags().String("server", defaultRunnerServer, "Control-plane URL")
		command.Flags().String("auth-token", os.Getenv("KODELET_AUTH_TOKEN"), "Control-plane API authentication token (or KODELET_AUTH_TOKEN)")
		command.Flags().Bool("json", false, "Output in JSON format")
	}
	runnerCmd.AddCommand(runnerStartCmd, runnerListCmd, runnerInspectCmd)
	rootCmd.AddCommand(runnerCmd)
}

func runnerQueryConfigFromFlags(cmd *cobra.Command) runnerQueryConfig {
	server, _ := cmd.Flags().GetString("server")
	authToken, _ := cmd.Flags().GetString("auth-token")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	return runnerQueryConfig{Server: server, AuthToken: authToken, JSONOutput: jsonOutput}
}

func runRunnerStart(ctx context.Context, config runnerStartConfig) error {
	workspace, err := os.Getwd()
	if err != nil {
		return errors.Wrap(err, "failed to determine current workspace")
	}
	// The runner credential is retained in memory for the control socket. Neither
	// control-plane credential belongs in extension or workspace process environments.
	scrubRunnerCredentialEnvironment()

	var registered bool
	runner, err := runnerclient.NewRunner(ctx, runnerclient.RunnerConfig{
		Server:      config.Server,
		AuthToken:   config.AuthToken,
		Workspace:   workspace,
		DisplayName: config.DisplayName,
		OnRegistered: func(result protocol.RegisterResult) {
			if registered {
				presenter.Success(fmt.Sprintf("Runner reconnected as %s", result.RunnerID))
				return
			}
			registered = true
			presenter.Success(fmt.Sprintf("Runner registered as %s", result.RunnerID))
		},
		OnRetry: func(connectionErr error, delay time.Duration) {
			presenter.Warning(fmt.Sprintf("Runner connection lost: %v; retrying in %s", connectionErr, delay))
		},
	})
	if err != nil {
		return err
	}

	runCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	presenter.Info(fmt.Sprintf("Binding runner to workspace: %s", workspace))
	presenter.Info(fmt.Sprintf("Connecting to control plane: %s", config.Server))
	presenter.Info("Press Ctrl+C to stop the runner")
	if err := runner.Run(runCtx); err != nil {
		return err
	}
	presenter.Info("Runner stopped")
	return nil
}

func scrubRunnerCredentialEnvironment() {
	_ = os.Unsetenv("KODELET_RUNNER_AUTH_TOKEN")
	_ = os.Unsetenv("KODELET_AUTH_TOKEN")
}

func runRunnerList(ctx context.Context, config runnerQueryConfig, output io.Writer) error {
	runners, server, err := fetchRunners(ctx, config.Server, config.AuthToken)
	if err != nil {
		return err
	}
	if config.JSONOutput {
		return writeRunnerJSON(output, runnerListAPIResponse{Runners: runners})
	}
	if len(runners) == 0 {
		presenter.Info(fmt.Sprintf("No runners registered with %s", server))
		return nil
	}
	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tName\tHost\tStatus\tWorkspace\tVersion\tActive run\tManifest changed\tLast heartbeat")
	for _, runner := range runners {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%t\t%s\n",
			runner.ID,
			runnerDisplayName(runner),
			runner.Host.Hostname,
			runner.Status,
			runner.Workspace.Path,
			runner.KodeletVersion,
			runner.ActiveRunID,
			runner.ManifestChanged,
			formatRunnerTime(runner.LastHeartbeatAt),
		)
	}
	return tw.Flush()
}

func runRunnerInspect(ctx context.Context, query string, config runnerQueryConfig, output io.Writer) error {
	runners, server, err := fetchRunners(ctx, config.Server, config.AuthToken)
	if err != nil {
		return err
	}
	runner, err := selectRunner(runners, query)
	if err != nil {
		return err
	}
	result := runnerInspectOutput{Runner: runner}
	if store, storeErr := localstate.NewStore(); storeErr == nil {
		registrations, registrationErr := store.Registrations()
		if registrationErr == nil {
			for _, registration := range registrations {
				if registration.Server != server || registration.RunnerID != runner.ID {
					continue
				}
				local := &runnerLocalOutput{LockPath: store.WorkspaceLockPath(registration.Workspace)}
				if metadata, found, metadataErr := store.ReadWorkspaceLockMetadata(registration.Workspace); metadataErr == nil && found {
					local.Metadata = &metadata
				}
				result.Local = local
				break
			}
		}
	}
	if config.JSONOutput {
		return writeRunnerJSON(output, result)
	}
	return renderRunnerInspect(output, result)
}

func fetchRunners(ctx context.Context, rawServer, authToken string) ([]runnerregistry.Runner, string, error) {
	server, err := normalizeRunnerAPIBaseURL(rawServer)
	if err != nil {
		return nil, "", err
	}
	parsed, _ := url.Parse(server)
	parsed.Path = path.Join(parsed.Path, "/api/runners")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to create runner list request")
	}
	if token := strings.TrimSpace(authToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to query control-plane runners")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", errors.Errorf("control plane returned HTTP %d while listing runners", response.StatusCode)
	}
	var payload runnerListAPIResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, "", errors.Wrap(err, "failed to decode runner list response")
	}
	sort.Slice(payload.Runners, func(i, j int) bool { return payload.Runners[i].ID < payload.Runners[j].ID })
	return payload.Runners, server, nil
}

func normalizeRunnerAPIBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("control-plane URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse control-plane URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("control-plane URL must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("control-plane URL must contain only scheme, host, and an optional base path")
	}
	if parsed.Scheme == "http" && !runnerLoopbackHostname(parsed.Hostname()) {
		return "", errors.New("remote control-plane connections require https; http is allowed only for loopback servers")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String(), nil
}

func runnerLoopbackHostname(hostname string) bool {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func selectRunner(runners []runnerregistry.Runner, query string) (runnerregistry.Runner, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return runnerregistry.Runner{}, errors.New("runner selector is required")
	}
	for _, runner := range runners {
		if runner.ID == query {
			return runner, nil
		}
	}
	matches := make([]runnerregistry.Runner, 0)
	for _, runner := range runners {
		if strings.HasPrefix(runner.ID, query) || runnerDisplayName(runner) == query {
			matches = append(matches, runner)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return runnerregistry.Runner{}, errors.Errorf("runner not found: %s", query)
	}
	details := make([]string, 0, len(matches))
	for _, runner := range matches {
		details = append(details, fmt.Sprintf("%s (%s, %s, %s)", runner.ID, runnerDisplayName(runner), runner.Host.Hostname, runner.Workspace.Path))
	}
	return runnerregistry.Runner{}, errors.Errorf("runner selector %q is ambiguous: %s", query, strings.Join(details, "; "))
}

func runnerDisplayName(runner runnerregistry.Runner) string {
	if strings.TrimSpace(runner.DisplayName) != "" {
		return runner.DisplayName
	}
	return runner.Workspace.Name
}

func renderRunnerInspect(output io.Writer, result runnerInspectOutput) error {
	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	runner := result.Runner
	fmt.Fprintf(tw, "ID:\t%s\n", runner.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", runnerDisplayName(runner))
	fmt.Fprintf(tw, "Status:\t%s\n", runner.Status)
	fmt.Fprintf(tw, "Connected:\t%t\n", runner.Connected)
	fmt.Fprintf(tw, "Workspace:\t%s\n", runner.Workspace.Path)
	fmt.Fprintf(tw, "Hostname:\t%s\n", runner.Host.Hostname)
	fmt.Fprintf(tw, "Host instance:\t%s\n", runner.Host.InstanceID)
	fmt.Fprintf(tw, "PID:\t%d\n", runner.Host.PID)
	fmt.Fprintf(tw, "Platform:\t%s/%s\n", runner.Host.OS, runner.Host.Arch)
	fmt.Fprintf(tw, "Kodelet version:\t%s\n", runner.KodeletVersion)
	fmt.Fprintf(tw, "Manifest digest:\t%s\n", runner.ManifestDigest)
	fmt.Fprintf(tw, "Manifest changed:\t%t\n", runner.ManifestChanged)
	if runner.CompatibilityError != "" {
		fmt.Fprintf(tw, "Compatibility error:\t%s\n", runner.CompatibilityError)
	}
	fmt.Fprintf(tw, "Active run:\t%s\n", runner.ActiveRunID)
	fmt.Fprintf(tw, "Connection ID:\t%s\n", runner.ConnectionID)
	fmt.Fprintf(tw, "Generation:\t%d\n", runner.Generation)
	fmt.Fprintf(tw, "Connected at:\t%s\n", formatRunnerTime(runner.ConnectedAt))
	fmt.Fprintf(tw, "Last heartbeat:\t%s\n", formatRunnerTime(runner.LastHeartbeatAt))
	if result.Local != nil {
		fmt.Fprintf(tw, "Local lock:\t%s\n", result.Local.LockPath)
		if result.Local.Metadata != nil {
			fmt.Fprintf(tw, "Local start time:\t%s\n", formatRunnerTime(result.Local.Metadata.StartedAt))
			if result.Local.Metadata.StoppedAt != nil {
				fmt.Fprintf(tw, "Local stop time:\t%s\n", formatRunnerTime(*result.Local.Metadata.StoppedAt))
			}
		}
	}
	return tw.Flush()
}

func writeRunnerJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func formatRunnerTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
