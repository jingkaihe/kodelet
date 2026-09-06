package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Execute a one-shot query through the Kodelet daemon and save the conversation.`,
	Args:  cobra.MinimumNArgs(0),
	RunE:  runControlPlaneCommand,
}

func init() {
	addRunFlags(runCmd)
}

func addRunFlags(cmd *cobra.Command) {
	addRemoteRunFlags(cmd)
	cmd.Flags().String("resume", "", "Resume a specific daemon conversation")
	cmd.Flags().String("cwd", "", "Working directory on the runner (current directory for the same-host default)")
	cmd.Flags().BoolP("follow", "f", false, "Follow the most recent conversation in the selected workspace")
	cmd.Flags().StringSliceP("image", "I", nil, "Add a client image attachment (can be repeated)")
	cmd.Flags().Int("max-turns", 0, "Maximum number of agentic turns (0 for no limit)")
	cmd.Flags().StringP("recipe", "r", "", "Use a runner-owned recipe")
	cmd.Flags().StringToString("arg", nil, "Recipe arguments (e.g. --arg name=John)")
	cmd.Flags().StringSlice("fragment-dirs", nil, "Unsupported client recipe directories; configure the runner instead")
	cmd.Flags().Bool("no-extensions", false, "Disable extensions for this environment")
	cmd.Flags().Bool("no-tools", false, "Disable all tools")
	cmd.Flags().Bool("enable-fs-search-tools", false, "Enable filesystem search tools")
	cmd.Flags().Bool("result-only", false, "Print only the final agent message")
	cmd.Flags().Bool("use-weak-model", false, "Use the daemon's weak model")
	cmd.Flags().String("account", "", "Unsupported client account override; select a daemon profile instead")
}

func formatFragmentDisplayArgs(args map[string]string) string {
	keys := make([]string, 0, len(args))
	for key := range args {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := args[key]
		if strings.ContainsAny(value, " \t\n\r\"") {
			value = fmt.Sprintf("%q", value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " ")
}

func getQueryFromStdinOrArgs(args []string) (string, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", errors.Wrap(err, "failed to inspect stdin")
	}
	if stat.Mode()&os.ModeCharDevice == 0 {
		stdinBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", errors.Wrap(err, "failed to read from stdin")
		}
		if len(args) > 0 {
			return strings.Join(args, " ") + "\n" + string(stdinBytes), nil
		}
		return string(stdinBytes), nil
	}
	if len(args) == 0 {
		return "", errors.New("no query provided")
	}
	return strings.Join(args, " "), nil
}
