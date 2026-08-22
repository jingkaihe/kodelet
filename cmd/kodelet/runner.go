package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
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

type runnerEnrollConfig struct {
	Server                 string
	DisplayName            string
	ReplaceLocalCredential bool
	NoBrowser              bool
	Store                  *localstate.Store
	HTTPClient             *http.Client
	OpenBrowser            func(string) error
}

type runnerQueryConfig struct {
	Server      string
	AuthToken   string
	JSONOutput  bool
	ConfigError error
}

type runnerRemoveConfig struct {
	runnerQueryConfig
	Force     bool
	NoConfirm bool
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
	Long:  "Start, inspect, and remove workspace-bound runner processes connected to a Kodelet control plane.",
}

var runnerStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a runner bound to the current workspace",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRunnerStart(cmd.Context(), runnerStartConfigFromFlags(cmd))
	},
}

var runnerEnrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Enroll the current workspace runner with a control plane",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRunnerEnroll(cmd.Context(), runnerEnrollConfigFromFlags(cmd), os.Stdout)
	},
}

var runnerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List runners registered with a control plane",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRunnerList(cmd.Context(), runnerQueryConfigFromFlags(cmd), os.Stdout)
	},
}

var runnerInspectCmd = &cobra.Command{
	Use:   "inspect <runner>",
	Short: "Inspect a runner by ID, ID prefix, or display name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRunnerInspect(cmd.Context(), args[0], runnerQueryConfigFromFlags(cmd), os.Stdout)
	},
}

var runnerRemoveCmd = &cobra.Command{
	Use:   "remove <runner>",
	Short: "Remove an offline runner registration",
	Long:  "Remove an offline runner registration and its durable runner-run history.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		config := runnerRemoveConfig{runnerQueryConfig: runnerQueryConfigFromFlags(cmd)}
		config.Force, _ = cmd.Flags().GetBool("force")
		config.NoConfirm, _ = cmd.Flags().GetBool("no-confirm")
		return runRunnerRemove(cmd.Context(), args[0], config, os.Stdin, os.Stdout)
	},
}

func init() {
	runnerStartCmd.Flags().String("server", defaultRunnerServer, "Control-plane URL")
	runnerStartCmd.Flags().String("auth-token", "", "Runner-only authentication token (or KODELET_RUNNER_AUTH_TOKEN)")
	runnerStartCmd.Flags().String("name", "", "Optional mutable display name")
	runnerEnrollCmd.Flags().String("server", defaultRunnerServer, "Control-plane URL")
	runnerEnrollCmd.Flags().String("name", "", "Optional mutable display name")
	runnerEnrollCmd.Flags().Bool("replace", false, "Replace an existing local runner credential after browser approval")
	runnerEnrollCmd.Flags().Bool("no-browser", false, "Do not open the browser automatically")
	for _, command := range []*cobra.Command{runnerListCmd, runnerInspectCmd, runnerRemoveCmd} {
		command.Flags().String("server", defaultRunnerServer, "Control-plane URL")
		command.Flags().String("auth-token", "", "Control-plane API authentication token (or KODELET_AUTH_TOKEN)")
		command.Flags().Bool("json", false, "Output in JSON format")
	}
	runnerRemoveCmd.Flags().Bool("force", false, "Accepted for compatibility; runner affinity is cleared on every removal")
	runnerRemoveCmd.Flags().Bool("no-confirm", false, "Skip the removal confirmation prompt")
	runnerCmd.AddCommand(runnerStartCmd, runnerEnrollCmd, runnerListCmd, runnerInspectCmd, runnerRemoveCmd)
	rootCmd.AddCommand(runnerCmd)
}

func runnerStartConfigFromFlags(cmd *cobra.Command) runnerStartConfig {
	server, _ := serverFlagOrConfig(cmd)
	displayName, _ := cmd.Flags().GetString("name")
	return runnerStartConfig{
		Server:      server,
		AuthToken:   authTokenFlagOrEnvironment(cmd, runnerAuthTokenEnv),
		DisplayName: displayName,
	}
}

func runnerEnrollConfigFromFlags(cmd *cobra.Command) runnerEnrollConfig {
	server, _ := serverFlagOrConfig(cmd)
	displayName, _ := cmd.Flags().GetString("name")
	replace, _ := cmd.Flags().GetBool("replace")
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	return runnerEnrollConfig{
		Server:                 server,
		DisplayName:            strings.TrimSpace(displayName),
		ReplaceLocalCredential: replace,
		NoBrowser:              noBrowser,
	}
}

func runnerQueryConfigFromFlags(cmd *cobra.Command) runnerQueryConfig {
	server, _ := serverFlagOrConfig(cmd)
	jsonOutput, _ := cmd.Flags().GetBool("json")
	authToken, _, authErr := resolveControlPlaneAuthToken(cmd, server)
	return runnerQueryConfig{
		Server:      server,
		AuthToken:   authToken,
		JSONOutput:  jsonOutput,
		ConfigError: authErr,
	}
}

func runRunnerStart(ctx context.Context, config runnerStartConfig) error {
	workspace, err := os.Getwd()
	if err != nil {
		return errors.Wrap(err, "failed to determine current workspace")
	}
	if err := os.Setenv(controlPlaneServerEnv, strings.TrimSpace(config.Server)); err != nil {
		return errors.Wrap(err, "failed to expose the control-plane server to runner subprocesses")
	}

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

func runRunnerEnroll(ctx context.Context, config runnerEnrollConfig, output io.Writer) error {
	workspace, err := os.Getwd()
	if err != nil {
		return errors.Wrap(err, "failed to determine current workspace")
	}
	if output == nil {
		output = io.Discard
	}
	openBrowser := config.OpenBrowser
	if openBrowser == nil {
		openBrowser = osutil.OpenBrowser
	}

	result, err := runnerclient.EnrollRunner(ctx, runnerclient.EnrollmentConfig{
		Server:                 config.Server,
		Workspace:              workspace,
		DisplayName:            config.DisplayName,
		Store:                  config.Store,
		HTTPClient:             config.HTTPClient,
		ReplaceLocalCredential: config.ReplaceLocalCredential,
		OnPending: func(info runnerclient.EnrollmentInfo) {
			if info.Resumed {
				fmt.Fprintln(output, "Resuming pending runner enrollment")
			} else {
				fmt.Fprintln(output, "Runner enrollment started")
			}
			fmt.Fprintf(output, "Enrollment code: %s\n", info.UserCode)
			fmt.Fprintf(output, "Public-key fingerprint: %s\n", info.Fingerprint)
			fmt.Fprintf(output, "Enter this code at: %s\n", info.VerificationURL)
			if !config.NoBrowser {
				if browserErr := openBrowser(info.VerificationURL); browserErr != nil {
					fmt.Fprintf(output, "Could not open the browser automatically: %v\n", browserErr)
				}
			}
			fmt.Fprintln(output, "Waiting for browser approval...")
		},
	})
	if err != nil {
		if errors.Is(err, runnerclient.ErrActiveLocalCredential) {
			return errors.Wrap(err, "use --replace to enroll a replacement key")
		}
		return err
	}

	fmt.Fprintln(output, "Runner enrollment approved")
	fmt.Fprintf(output, "Runner ID: %s\n", result.RunnerID)
	fmt.Fprintf(output, "Credential fingerprint: %s\n", result.Fingerprint)
	fmt.Fprintln(output, "Start the enrolled runner with `kodelet runner start`")
	return nil
}

func runRunnerList(ctx context.Context, config runnerQueryConfig, output io.Writer) error {
	if config.ConfigError != nil {
		return config.ConfigError
	}
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
	fmt.Fprintln(tw, "ID\tName\tHost\tStatus\tWorkspace\tVersion\tActive runs\tManifest changed\tLast heartbeat")
	for _, runner := range runners {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%t\t%s\n",
			runner.ID,
			runnerDisplayName(runner),
			runner.Host.Hostname,
			runner.Status,
			runner.Workspace.Path,
			runner.KodeletVersion,
			strings.Join(activeRunnerRunIDs(runner), ", "),
			runner.ManifestChanged,
			formatRunnerTime(runner.LastHeartbeatAt),
		)
	}
	return tw.Flush()
}

func runRunnerInspect(ctx context.Context, query string, config runnerQueryConfig, output io.Writer) error {
	if config.ConfigError != nil {
		return config.ConfigError
	}
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

func runRunnerRemove(ctx context.Context, query string, config runnerRemoveConfig, input io.Reader, output io.Writer) error {
	if config.ConfigError != nil {
		return config.ConfigError
	}
	if config.JSONOutput && !config.NoConfirm {
		return errors.New("--json requires --no-confirm for runner removal")
	}
	runners, server, err := fetchRunners(ctx, config.Server, config.AuthToken)
	if err != nil {
		return err
	}
	runner, err := selectRunner(runners, query)
	if err != nil {
		return err
	}
	if runner.Connected {
		return errors.Errorf("runner %s is connected; stop it before removal", runner.ID)
	}
	if !config.NoConfirm && !confirmRunnerRemoval(input, output, runner, config.Force) {
		fmt.Fprintln(output, "Runner removal cancelled")
		return nil
	}

	result, err := deleteRunner(ctx, server, config.AuthToken, runner.ID, config.Force)
	if err != nil {
		return err
	}
	if err := deleteLocalRunnerRegistration(server, runner); err != nil {
		logger.G(ctx).WithError(err).WithField("runner_id", runner.ID).Warn("failed to remove local runner authentication state")
	}
	if config.JSONOutput {
		return writeRunnerJSON(output, result)
	}
	fmt.Fprintf(output, "Removed runner %s (%d run record(s), %d conversation binding(s))\n", result.RunnerID, result.RemovedRuns, result.RemovedConversationAffinities)
	return nil
}

func confirmRunnerRemoval(input io.Reader, output io.Writer, runner runnerregistry.Runner, force bool) bool {
	_ = force
	fmt.Fprintf(output, "Remove runner %s (%s on %s)? ", runner.ID, runner.Workspace.Path, runner.Host.Hostname)
	fmt.Fprint(output, "This deletes its registration, credentials, and runner run history. Conversation transcripts are preserved, while bindings to this runner are cleared; future remote execution requires selecting a compatible runner. ")
	fmt.Fprint(output, "[y/N]: ")
	response, _ := bufio.NewReader(input).ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

func deleteRunner(ctx context.Context, server, authToken, runnerID string, force bool) (runnerregistry.RemovalResult, error) {
	endpoint, err := controlplaneurl.Endpoint(server, "api", "runners", runnerID)
	if err != nil {
		return runnerregistry.RemovalResult{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return runnerregistry.RemovalResult{}, errors.Wrap(err, "failed to parse runner removal URL")
	}
	if force {
		query := parsed.Query()
		query.Set("force", strconv.FormatBool(true))
		parsed.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, parsed.String(), nil)
	if err != nil {
		return runnerregistry.RemovalResult{}, errors.Wrap(err, "failed to create runner removal request")
	}
	if token := strings.TrimSpace(authToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return runnerregistry.RemovalResult{}, errors.Wrap(err, "failed to remove control-plane runner")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(response.Body).Decode(&payload)
		if strings.TrimSpace(payload.Error) != "" {
			return runnerregistry.RemovalResult{}, errors.Errorf("control plane rejected runner removal: %s", payload.Error)
		}
		return runnerregistry.RemovalResult{}, errors.Errorf("control plane returned HTTP %d while removing runner", response.StatusCode)
	}
	var result runnerregistry.RemovalResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return runnerregistry.RemovalResult{}, errors.Wrap(err, "failed to decode runner removal response")
	}
	return result, nil
}

func deleteLocalRunnerRegistration(server string, runner runnerregistry.Runner) error {
	store, err := localstate.NewStore()
	if err != nil {
		return err
	}
	registrations, err := store.Registrations()
	if err != nil {
		return err
	}
	for _, registration := range registrations {
		if registration.Server == server && registration.RunnerID == runner.ID {
			if _, err := store.DeleteAuthenticationStateForRegistration(registration.Server, registration.Workspace, runner.ID); err != nil {
				return errors.Wrap(err, "failed to delete local runner authentication state")
			}
			return nil
		}
	}
	return nil
}

func fetchRunners(ctx context.Context, rawServer, authToken string) ([]runnerregistry.Runner, string, error) {
	server, err := normalizeRunnerAPIBaseURL(rawServer)
	if err != nil {
		return nil, "", err
	}
	endpoint, err := controlplaneurl.Endpoint(server, "api", "runners")
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
	return controlplaneurl.NormalizeBase(raw)
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

func activeRunnerRunIDs(runner runnerregistry.Runner) []string {
	if len(runner.ActiveRunIDs) > 0 {
		return runner.ActiveRunIDs
	}
	if strings.TrimSpace(runner.ActiveRunID) != "" {
		return []string{runner.ActiveRunID}
	}
	return nil
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
	fmt.Fprintf(tw, "Active runs:\t%s\n", strings.Join(activeRunnerRunIDs(runner), ", "))
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
