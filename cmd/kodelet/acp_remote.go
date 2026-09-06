package main

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/acp"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// daemonACPProvider has no runner lifecycle or local workspace state. An empty
// selection lets new sessions use the daemon default and resumed sessions use
// their stored runner, even when that default is unavailable or has changed.
type daemonACPProvider struct {
	client   *chat.ControlPlaneChatRunner
	runnerID string
}

func (p *daemonACPProvider) WaitForRemoteChat(context.Context) (acp.RemoteChatClient, string, error) {
	return p.client, p.runnerID, nil
}

func runRemoteACP(ctx context.Context, cmd *cobra.Command, serverURL string) error {
	config, err := remoteACPSessionConfig(ctx, cmd, serverURL)
	if err != nil {
		return errors.Wrap(err, "cannot connect ACP to daemon; start kodelet serve or check --server and client authentication (no local fallback)")
	}
	server := acp.NewServer(acp.WithContext(ctx), acp.WithInput(cmd.InOrStdin()), acp.WithOutput(cmd.OutOrStdout()), acp.WithRemoteSessions(config))
	return runACPServer(ctx, server)
}

func remoteACPSessionConfig(ctx context.Context, cmd *cobra.Command, serverURL string) (acp.RemoteSessionConfig, error) {
	var config acp.RemoteSessionConfig
	if err := validateRemoteACPFlags(cmd); err != nil {
		return config, err
	}
	options, err := remoteRunExecutionOptions(cmd)
	if err != nil {
		return config, err
	}
	token, _, err := resolveControlPlaneAuthToken(cmd, serverURL)
	if err != nil {
		return config, err
	}
	client, err := chat.NewControlPlaneChatRunner(serverURL, token, "")
	if err != nil {
		return config, err
	}
	provider := &daemonACPProvider{client: client}
	selector, _ := cmd.Flags().GetString("runner")
	if strings.TrimSpace(selector) != "" {
		runners, _, err := fetchRunners(ctx, serverURL, token)
		if err != nil {
			return config, err
		}
		runner, err := selectRunner(runners, selector)
		if err != nil {
			return config, err
		}
		// Readiness and workspaceDiscovery capability are checked centrally on
		// discovery, not by starting or acquiring a client-owned workspace.
		provider.runnerID = runner.ID
	}
	config.Provider = provider
	config.Options = options
	if cmd.Flags().Changed("profile") {
		config.Profile, _ = cmd.Flags().GetString("profile")
	}
	config.EnvironmentProfile, _ = cmd.Flags().GetString("runner-profile")
	config.EnvironmentProfileExplicit = cmd.Flags().Changed("runner-profile")
	return config, nil
}

func validateRemoteACPFlags(cmd *cobra.Command) error {
	for _, name := range []string{"runner-auth-token", "sysprompt", "sysprompt-arg", "allowed-domains-file", "anthropic-api-access", "tool-mode", "context-patterns", "compact-ratio", "enable-openai-search"} {
		if cmd.Flags().Changed(name) {
			return errors.Errorf("--%s is not supported by daemon-backed ACP; configure it on the owning daemon or runner", name)
		}
	}
	return nil
}
