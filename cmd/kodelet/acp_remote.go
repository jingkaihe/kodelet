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
	client   *chat.Client
	runnerID string
}

func (p *daemonACPProvider) WaitForRemoteChat(context.Context) (acp.RemoteChatClient, string, error) {
	return p.client, p.runnerID, nil
}

func runRemoteACP(ctx context.Context, cmd *cobra.Command, serverURL string) error {
	config, err := remoteACPSessionConfig(ctx, cmd, serverURL)
	if err != nil {
		return errors.Wrap(err, "could not start the ACP connection")
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
	// Share chat/run bootstrap and saved OIDC login handling, while preserving
	// library callers that supply a different serverURL directly.
	var token string
	if selected, _ := serverFlagOrConfig(cmd); selected == serverURL {
		serverURL, token, err = prepareClientServer(ctx, cmd)
	} else {
		token, _, err = resolveControlPlaneAuthToken(cmd, serverURL)
	}
	if err != nil {
		return config, err
	}
	var runnerID string
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
		runnerID = runner.ID
	}
	client, err := chat.NewClient(serverURL, token, runnerID)
	if err != nil {
		return config, err
	}
	config.Provider = &daemonACPProvider{client: client, runnerID: runnerID}
	config.Options = options
	if cmd.Flags().Changed("profile") {
		config.Profile, _ = cmd.Flags().GetString("profile")
	}
	config.EnvironmentProfile, _ = cmd.Flags().GetString("runner-profile")
	config.EnvironmentProfileExplicit = cmd.Flags().Changed("runner-profile")
	return config, nil
}

func validateRemoteACPFlags(cmd *cobra.Command) error {
	if cmd.Flags().Changed("runner-auth-token") {
		return errors.New("--runner-auth-token is not used by ACP; use --auth-token or 'kodelet auth login' to authenticate")
	}
	for _, name := range []string{"sysprompt", "sysprompt-arg", "allowed-domains-file", "anthropic-api-access", "tool-mode", "context-patterns", "compact-ratio", "enable-openai-search"} {
		if cmd.Flags().Changed(name) {
			return errors.Errorf("--%s cannot be set with 'kodelet acp'; set it in the server or runner configuration", name)
		}
	}
	return nil
}
