package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var remoteProfileCmd = &cobra.Command{
	Use:               "profile",
	Short:             "View available model profiles",
	Long:              "View model profiles available on the selected server. Use --profile when starting a conversation. To change the defaults, run 'kodelet host profile' on the server host and restart 'kodelet serve'.",
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

func init() {
	addRemoteAdministrationFlags(remoteProfileCmd)
	descriptions := map[string]string{
		"current": "Show the default model profile",
		"list":    "List available model profiles",
		"show":    "Show a model profile's reasoning settings",
	}
	for _, name := range []string{"current", "list", "show"} {
		command := &cobra.Command{Use: name, Short: descriptions[name], Args: cobra.NoArgs, RunE: runRemoteProfileCommand}
		if name == "show" {
			command.Use = "show <profile>"
			command.Args = cobra.ExactArgs(1)
			command.Flags().StringP("format", "f", "json", "Output format (json, yaml)")
		}
		remoteProfileCmd.AddCommand(command)
	}
	use := &cobra.Command{Use: "use <profile>", Short: "Show how to select a model profile", Args: cobra.ExactArgs(1), RunE: func(*cobra.Command, []string) error {
		return errors.New("select a profile with --profile when starting a conversation; to change the default, run 'kodelet host profile use <profile> -g' on the server host and restart 'kodelet serve'")
	}}
	use.Flags().BoolP("global", "g", false, "No longer supported here; use 'kodelet host profile use -g' on the server host")
	remoteProfileCmd.AddCommand(use)
}

func addRemoteAdministrationFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("server", defaultRunnerServer, "Server URL (or KODELET_SERVER)")
	cmd.PersistentFlags().String("auth-token", "", "API authentication token (or KODELET_AUTH_TOKEN)")
}

func remoteAdministrationClient(cmd *cobra.Command) (*chat.ControlPlaneChatRunner, error) {
	server, _ := serverFlagOrConfig(cmd)
	token, _, err := resolveControlPlaneAuthToken(cmd, server)
	if err != nil {
		return nil, err
	}
	return chat.NewControlPlaneChatRunner(server, token, "")
}

func runRemoteProfileCommand(cmd *cobra.Command, args []string) error {
	format := "json"
	profile := ""
	if cmd.Name() == "show" {
		profile = strings.TrimSpace(args[0])
		format, _ = cmd.Flags().GetString("format")
		if profile == "" {
			return errors.New("profile is required")
		}
		if format != "json" && format != "yaml" {
			return errors.New("profile format must be json or yaml")
		}
	}
	client, err := remoteAdministrationClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	settings, err := client.ChatSettings(ctx, profile)
	if err != nil {
		return errors.Wrap(err, "could not load model profiles")
	}
	current := settings.CurrentProfile
	if current == "" {
		current = "default"
	}
	switch cmd.Name() {
	case "current":
		_, err = fmt.Fprintln(cmd.OutOrStdout(), current)
	case "list":
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tSCOPE\tACTIVE")
		for _, option := range settings.Profiles {
			fmt.Fprintf(writer, "%s\t%s\t%t\n", option.Name, option.Scope, option.Active)
		}
		err = writer.Flush()
	case "show":
		if current != profile {
			return errors.New("the server returned a different profile than requested; use 'kodelet profile list' to check available profiles")
		}
		view := struct {
			Profile                string   `json:"profile" yaml:"profile"`
			ReasoningEffort        string   `json:"reasoningEffort" yaml:"reasoningEffort"`
			ReasoningEffortOptions []string `json:"reasoningEffortOptions" yaml:"reasoningEffortOptions"`
		}{current, settings.ReasoningEffort, settings.ReasoningEffortOptions}
		if format == "yaml" {
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			defer encoder.Close()
			err = encoder.Encode(view)
		} else {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			err = encoder.Encode(view)
		}
	}
	return err
}
