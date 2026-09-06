package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type acpServerLifecycle interface {
	Run() error
	Shutdown()
}

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Connect an ACP client to a Kodelet daemon",
	Long: `Run the Agent Client Protocol (ACP) stdio adapter for kodelet serve.

The daemon owns model execution and conversations. A registered runner owns
workspace tools, context, skills, recipes, and extensions. ACP never starts a
runner or falls back to a client-local provider or conversation database.
Session directories are interpreted and validated on the selected runner.

Examples:
  kodelet acp
  kodelet acp --server https://kodelet.example --runner workstation
  kodelet acp --profile coding --max-turns 10 --no-tools`,
	Args: cobra.NoArgs,
	// ACP is a thin client even without an explicit --server. Do not inherit
	// the root hook that installs workspace binaries and migrates a local DB.
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return validateRemoteACPFlags(cmd) },
	RunE:              runACP,
}

func init() {
	rootCmd.AddCommand(acpCmd)
	addRemoteRunFlags(acpCmd)
	acpCmd.Flags().Bool("no-extensions", false, "Disable runner extensions for this execution")
	acpCmd.Flags().Bool("no-tools", false, "Disable all model-callable tools")
	acpCmd.Flags().Bool("use-weak-model", false, "Use the daemon's configured weak model")
	acpCmd.Flags().Int("max-turns", 0, "Maximum agentic turns per prompt (0 for no limit)")
	// Retain the old spelling only to give an actionable migration error.
	acpCmd.Flags().String("runner-auth-token", "", "Removed: configure credentials on the runner, not ACP")
	_ = acpCmd.Flags().MarkHidden("runner-auth-token")
}

func runACP(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.SetLogOutput(os.Stderr)
	logger.SetLogLevel(viper.GetString("log_level"))
	serverURL, _ := serverFlagOrConfig(cmd)
	return runRemoteACP(ctx, cmd, serverURL)
}

func runACPServer(ctx context.Context, server acpServerLifecycle) error {
	runErr := make(chan error, 1)
	go func() { runErr <- server.Run() }()
	select {
	case err := <-runErr:
		server.Shutdown()
		return err
	case <-ctx.Done():
		server.Shutdown()
		return nil
	}
}
