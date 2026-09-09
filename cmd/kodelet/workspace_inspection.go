package main

import (
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func addWorkspaceInspectionFlags(cmd *cobra.Command) {
	addRemoteAdministrationFlags(cmd)
	cmd.PersistentFlags().String("runner", "", "Runner ID, unique prefix or name (defaults to the server's built-in runner)")
	cmd.PersistentFlags().String("cwd", "", "Working directory on the runner (defaults to the current directory for a same-machine built-in runner)")
	cmd.PersistentFlags().String("profile", "", "Model profile whose workspace settings apply")
	cmd.PersistentFlags().String("runner-profile", "", "Runner environment profile")
}

func inspectCommandWorkspace(cmd *cobra.Command, params protocol.WorkspaceInspectParams) (protocol.WorkspaceInspectResult, error) {
	if err := validateWorkspaceInspectionFlags(cmd); err != nil {
		return protocol.WorkspaceInspectResult{}, err
	}
	client, err := remoteAdministrationClient(cmd)
	if err != nil {
		return protocol.WorkspaceInspectResult{}, err
	}
	target := chat.WorkspaceTarget{}
	target.RunnerID, _ = cmd.Flags().GetString("runner")
	target.CWD, _ = cmd.Flags().GetString("cwd")
	target.Profile, _ = cmd.Flags().GetString("profile")
	target.EnvironmentProfile, _ = cmd.Flags().GetString("runner-profile")
	if target.RunnerID != "" {
		runners, err := client.WorkspaceRunners(cmd.Context())
		if err != nil {
			return protocol.WorkspaceInspectResult{}, err
		}
		selected, err := selectRunner(runners, target.RunnerID)
		if err != nil {
			return protocol.WorkspaceInspectResult{}, err
		}
		target.RunnerID = selected.ID
	}
	configured := &configuredChatRunner{Client: client}
	target, err = configured.discoveryTarget(cmd.Context(), target)
	if err != nil {
		return protocol.WorkspaceInspectResult{}, err
	}
	return client.InspectWorkspace(cmd.Context(), target, params)
}

func validateWorkspaceInspectionFlags(cmd *cobra.Command) error {
	var invalid string
	cmd.InheritedFlags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Changed {
			return
		}
		switch flag.Name {
		case "server", "auth-token", "runner", "cwd", "profile", "runner-profile", "log-level", "log-format", "tracing-enabled", "tracing-ratio", "tracing-sampler":
		default:
			if invalid == "" {
				invalid = flag.Name
			}
		}
	})
	if invalid != "" {
		return errors.Errorf("--%s does not apply to workspace inspection; select configured workspace settings with --profile or --runner-profile", invalid)
	}
	return nil
}
