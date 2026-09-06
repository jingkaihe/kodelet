package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Execute a one-shot query with Kodelet",
	Long:  `Run a query and save the conversation. Start 'kodelet serve' first, or use --server to connect to an existing server.`,
	Args:  cobra.MinimumNArgs(0),
	RunE:  runControlPlaneCommand,
}

func init() {
	addRunFlags(runCmd)
}

func addRunFlags(cmd *cobra.Command) {
	addRemoteRunFlags(cmd)
	cmd.Flags().String("resume", "", "Resume a specific conversation")
	cmd.Flags().String("cwd", "", "Working directory on the runner (defaults to your current directory when using this machine's built-in runner)")
	cmd.Flags().BoolP("follow", "f", false, "Follow the most recent conversation in the selected workspace")
	cmd.Flags().StringSliceP("image", "I", nil, "Attach an image from this machine or an HTTPS URL (can be repeated)")
	cmd.Flags().Int("max-turns", 0, "Maximum AI turns (0 for no limit)")
	cmd.Flags().StringP("recipe", "r", "", "Use a recipe installed on the runner")
	cmd.Flags().StringToString("arg", nil, "Recipe arguments (e.g. --arg name=John)")
	cmd.Flags().StringSlice("fragment-dirs", nil, "No longer supported here; configure recipe directories on the runner")
	cmd.Flags().Bool("no-extensions", false, "Disable extensions for this environment")
	cmd.Flags().Bool("no-tools", false, "Disable all tools")
	cmd.Flags().Bool("enable-fs-search-tools", false, "Enable filesystem search tools")
	cmd.Flags().Bool("result-only", false, "Print only the final agent message")
	cmd.Flags().Bool("use-weak-model", false, "Use the configured weak model")
	cmd.Flags().String("account", "", "No longer supported here; select an account through --profile")
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
		if strings.ContainsAny(value, "\"\\") || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
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
