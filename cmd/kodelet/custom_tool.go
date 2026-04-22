package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const customToolInputJSONFlag = "input-json"

var customToolCmd = &cobra.Command{
	Use:   "custom-tool",
	Short: "List, inspect, and invoke custom tools",
	Long:  `Manage custom executable tools discovered by Kodelet.`,
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

var customToolListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered custom tools",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		manager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create custom tool manager")
		}

		jsonOutput, _ := cmd.Flags().GetBool("json")
		showPath, _ := cmd.Flags().GetBool("show-path")
		toolList := manager.ListCustomTools()

		if jsonOutput {
			return outputCustomToolsJSON(toolList)
		}

		if len(toolList) == 0 {
			presenter.Info("No custom tools found")
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if showPath {
			fmt.Fprintln(tw, "NAME\tDESCRIPTION\tPATH")
			fmt.Fprintln(tw, "----\t-----------\t----")
		} else {
			fmt.Fprintln(tw, "NAME\tDESCRIPTION")
			fmt.Fprintln(tw, "----\t-----------")
		}

		for _, tool := range toolList {
			if showPath {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", tool.RawName(), tool.Description(), tool.ExecPath())
				continue
			}
			fmt.Fprintf(tw, "%s\t%s\n", tool.RawName(), tool.Description())
		}

		return tw.Flush()
	},
}

var customToolDescribeCmd = &cobra.Command{
	Use:   "describe <tool>",
	Short: "Show a custom tool description and JSON schema",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		manager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create custom tool manager")
		}

		tool, ok := manager.GetTool(args[0])
		if !ok {
			return errors.Errorf("custom tool not found: %s", args[0])
		}

		payload := map[string]any{
			"name":         tool.RawName(),
			"description":  tool.Description(),
			"path":         tool.ExecPath(),
			"input_schema": tool.InputSchema(),
		}

		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	},
}

var customToolInvokeCmd = &cobra.Command{
	Use:                "invoke <tool>",
	Short:              "Invoke a custom tool directly",
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDynamicCustomToolInvoke(cmd, args)
	},
}

var customToolInvokeAliasCmd = &cobra.Command{
	Use:                "cti <tool>",
	Short:              "Alias for custom-tool invoke",
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDynamicCustomToolInvoke(cmd, args)
	},
}

func init() {
	customToolCmd.AddCommand(customToolListCmd)
	customToolCmd.AddCommand(customToolDescribeCmd)
	customToolCmd.AddCommand(customToolInvokeCmd)

	customToolListCmd.Flags().Bool("json", false, "Output in JSON format")
	customToolListCmd.Flags().Bool("show-path", false, "Show executable path for each tool")

	customToolDescribeCmd.ValidArgsFunction = completeCustomToolNames
	customToolInvokeCmd.ValidArgsFunction = completeCustomToolNames
	customToolInvokeAliasCmd.ValidArgsFunction = completeCustomToolNames
}

func outputCustomToolsJSON(toolList []*tools.CustomTool) error {
	type customToolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Path        string `json:"path"`
	}

	output := struct {
		Tools []customToolInfo `json:"tools"`
	}{
		Tools: make([]customToolInfo, 0, len(toolList)),
	}

	for _, tool := range toolList {
		output.Tools = append(output.Tools, customToolInfo{
			Name:        tool.RawName(),
			Description: tool.Description(),
			Path:        tool.ExecPath(),
		})
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func completeCustomToolNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	manager, err := tools.CreateCustomToolManagerFromViper(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	names := make([]string, 0)
	for _, tool := range manager.ListCustomTools() {
		if toComplete == "" || strings.HasPrefix(tool.RawName(), toComplete) {
			names = append(names, tool.RawName())
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

func runDynamicCustomToolInvoke(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		return cmd.Help()
	}

	ctx := cmd.Context()
	manager, err := tools.CreateCustomToolManagerFromViper(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create custom tool manager")
	}

	toolName, remainingArgs, err := splitDynamicCustomToolArgs(args)
	if err != nil {
		return err
	}

	tool, ok := manager.GetTool(toolName)
	if !ok {
		return errors.Errorf("custom tool not found: %s", toolName)
	}

	dynamicCmd, err := newDynamicCustomToolCommand(tool)
	if err != nil {
		return err
	}

	dynamicCmd.SetOut(cmd.OutOrStdout())
	dynamicCmd.SetErr(cmd.ErrOrStderr())
	dynamicCmd.SetContext(ctx)
	dynamicCmd.SilenceUsage = true
	dynamicCmd.SilenceErrors = true
	if len(remainingArgs) == 0 {
		remainingArgs = []string{"--help"}
	}
	dynamicCmd.SetArgs(remainingArgs)
	return dynamicCmd.ExecuteContext(ctx)
}

func splitDynamicCustomToolArgs(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, errors.New("custom tool name is required")
	}

	if args[0] == "--help" || args[0] == "-h" {
		return "", nil, errors.New("custom tool name is required")
	}

	toolName := args[0]
	remainingArgs := args[1:]

	for _, arg := range remainingArgs {
		if arg == "--help" || arg == "-h" {
			return toolName, []string{"--help"}, nil
		}
	}

	return toolName, remainingArgs, nil
}

func newDynamicCustomToolCommand(tool *tools.CustomTool) (*cobra.Command, error) {
	schema := tool.InputSchema()
	if schema == nil {
		return nil, errors.Errorf("custom tool %s has no input schema", tool.RawName())
	}
	if schema.Type != "" && schema.Type != "object" {
		return nil, errors.Errorf("custom tool %s input schema must have type object", tool.RawName())
	}

	required := make(map[string]struct{}, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = struct{}{}
	}

	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [flags]", tool.RawName()),
		Short: tool.Description(),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params, err := buildCustomToolInputFromFlags(cmd.Flags(), schema, required)
			if err != nil {
				return err
			}

			payload, err := json.Marshal(params)
			if err != nil {
				return errors.Wrap(err, "failed to encode tool input")
			}

			return executeCustomTool(cmd.Context(), tool, string(payload))
		},
	}

	cmd.Flags().SortFlags = false
	unsupportedFlags, err := addSchemaFlags(cmd, schema, required)
	if err != nil {
		return nil, err
	}
	cmd.Long = buildDynamicCustomToolLongDescription(tool, unsupportedFlags)

	cmd.Flags().String(customToolInputJSONFlag, "", "Raw JSON object merged on top of flag-derived input")
	return cmd, nil
}

func buildDynamicCustomToolLongDescription(tool *tools.CustomTool, unsupportedFlags []string) string {
	var b strings.Builder
	b.WriteString(tool.Description())
	b.WriteString("\n\nDynamically generated from the custom tool JSON schema.")
	b.WriteString("\nUse --input-json for nested or advanced JSON that does not map cleanly to flags.")
	if len(unsupportedFlags) > 0 {
		b.WriteString("\n\nProperties only available through --input-json: ")
		b.WriteString(strings.Join(unsupportedFlags, ", "))
	}
	return b.String()
}

func addSchemaFlags(cmd *cobra.Command, schema *jsonschema.Schema, required map[string]struct{}) ([]string, error) {
	if schema.Properties == nil || schema.Properties.Len() == 0 {
		return nil, nil
	}

	unsupportedFlags := make([]string, 0)

	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		name := pair.Key
		propertySchema := pair.Value
		if propertySchema == nil {
			continue
		}

		usage := buildSchemaFlagUsage(propertySchema, isRequired(name, required))
		supported, err := addSchemaFlag(cmd.Flags(), name, propertySchema, usage)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to add flag for property %s", name)
		}
		if !supported {
			unsupportedFlags = append(unsupportedFlags, name)
			continue
		}
	}

	return unsupportedFlags, nil
}

func buildSchemaFlagUsage(schema *jsonschema.Schema, required bool) string {
	parts := make([]string, 0, 3)
	if schema.Description != "" {
		parts = append(parts, schema.Description)
	}
	if schema.Type != "" {
		parts = append(parts, fmt.Sprintf("type: %s", schema.Type))
	}
	if len(schema.Enum) > 0 {
		parts = append(parts, fmt.Sprintf("allowed: %s", formatSchemaEnum(schema.Enum)))
	}
	if required {
		parts = append(parts, "required")
	}
	return strings.Join(parts, "; ")
}

func formatSchemaEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%v", value))
	}
	return strings.Join(parts, ", ")
}

func addSchemaFlag(flags *pflag.FlagSet, name string, schema *jsonschema.Schema, usage string) (bool, error) {
	switch schema.Type {
	case "", "string":
		defaultValue, err := schemaDefaultString(schema.Default)
		if err != nil {
			return false, err
		}
		flags.String(name, defaultValue, usage)
		return true, nil
	case "integer":
		defaultValue, err := schemaDefaultInt(schema.Default)
		if err != nil {
			return false, err
		}
		flags.Int(name, defaultValue, usage)
		return true, nil
	case "number":
		defaultValue, err := schemaDefaultFloat(schema.Default)
		if err != nil {
			return false, err
		}
		flags.Float64(name, defaultValue, usage)
		return true, nil
	case "boolean":
		defaultValue, err := schemaDefaultBool(schema.Default)
		if err != nil {
			return false, err
		}
		flags.Bool(name, defaultValue, usage)
		return true, nil
	case "array":
		if schema.Items == nil {
			return false, errors.New("array schema is missing items definition")
		}
		switch schema.Items.Type {
		case "", "string":
			defaultValue, err := schemaDefaultStringSlice(schema.Default)
			if err != nil {
				return false, err
			}
			flags.StringSlice(name, defaultValue, usage)
			return true, nil
		case "integer":
			defaultValue, err := schemaDefaultIntSlice(schema.Default)
			if err != nil {
				return false, err
			}
			flags.IntSlice(name, defaultValue, usage)
			return true, nil
		default:
			return false, nil
		}
	default:
		return false, nil
	}
}

func buildCustomToolInputFromFlags(flags *pflag.FlagSet, schema *jsonschema.Schema, required map[string]struct{}) (map[string]any, error) {
	params := make(map[string]any)

	if schema.Properties != nil {
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			name := pair.Key
			propertySchema := pair.Value
			if propertySchema == nil {
				continue
			}
			if flags.Lookup(name) == nil {
				continue
			}

			value, include, err := getFlagValue(flags, name, propertySchema, isRequired(name, required))
			if err != nil {
				return nil, errors.Wrapf(err, "failed to read flag %s", name)
			}
			if include {
				params[name] = value
			}
		}
	}

	rawJSON, _ := flags.GetString(customToolInputJSONFlag)
	if strings.TrimSpace(rawJSON) != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(rawJSON), &extra); err != nil {
			return nil, errors.Wrap(err, "invalid --input-json value")
		}
		for key, value := range extra {
			params[key] = value
		}
	}

	for name := range required {
		if _, ok := params[name]; !ok {
			return nil, errors.Errorf("missing required parameter: %s", name)
		}
	}

	return params, nil
}

func getFlagValue(flags *pflag.FlagSet, name string, schema *jsonschema.Schema, required bool) (any, bool, error) {
	flag := flags.Lookup(name)
	if flag == nil {
		return nil, false, errors.Errorf("flag not found: %s", name)
	}

	changed := flag.Changed
	includeDefault := required && schema.Default != nil
	include := changed || includeDefault

	switch schema.Type {
	case "", "string":
		value, err := flags.GetString(name)
		if err != nil {
			return nil, false, err
		}
		if !include {
			return nil, false, nil
		}
		return value, true, nil
	case "integer":
		value, err := flags.GetInt(name)
		if err != nil {
			return nil, false, err
		}
		if !include {
			return nil, false, nil
		}
		return value, true, nil
	case "number":
		value, err := flags.GetFloat64(name)
		if err != nil {
			return nil, false, err
		}
		if !include {
			return nil, false, nil
		}
		return value, true, nil
	case "boolean":
		value, err := flags.GetBool(name)
		if err != nil {
			return nil, false, err
		}
		if !include {
			return nil, false, nil
		}
		return value, true, nil
	case "array":
		if schema.Items == nil {
			return nil, false, errors.New("array schema is missing items definition")
		}
		switch schema.Items.Type {
		case "", "string":
			value, err := flags.GetStringSlice(name)
			if err != nil {
				return nil, false, err
			}
			if !include && len(value) == 0 {
				return nil, false, nil
			}
			return value, true, nil
		case "integer":
			value, err := flags.GetIntSlice(name)
			if err != nil {
				return nil, false, err
			}
			if !include && len(value) == 0 {
				return nil, false, nil
			}
			return value, true, nil
		default:
			return nil, false, errors.Errorf("unsupported array item type %q", schema.Items.Type)
		}
	default:
		return nil, false, errors.Errorf("unsupported schema type %q", schema.Type)
	}
}

func isRequired(name string, required map[string]struct{}) bool {
	_, ok := required[name]
	return ok
}

func executeCustomTool(ctx context.Context, tool *tools.CustomTool, inputJSON string) error {
	result := tool.Execute(ctx, nil, inputJSON)
	if result.IsError() {
		return errors.New(strings.TrimSpace(result.GetError()))
	}

	fmt.Fprintln(os.Stdout, strings.TrimRight(result.GetResult(), "\n"))
	return nil
}

func schemaDefaultString(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", errors.Errorf("default value %v is not a string", value)
	}
	return stringValue, nil
}

func schemaDefaultInt(value any) (int, error) {
	if value == nil {
		return 0, nil
	}
	switch v := value.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, errors.Errorf("default value %v is not an integer", value)
	}
}

func schemaDefaultFloat(value any) (float64, error) {
	if value == nil {
		return 0, nil
	}
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, errors.Errorf("default value %v is not a number", value)
	}
}

func schemaDefaultBool(value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	boolValue, ok := value.(bool)
	if !ok {
		return false, errors.Errorf("default value %v is not a boolean", value)
	}
	return boolValue, nil
}

func schemaDefaultStringSlice(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, errors.Errorf("default value %v is not an array", value)
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		stringValue, ok := item.(string)
		if !ok {
			return nil, errors.Errorf("array default value %v contains non-string item %v", value, item)
		}
		result = append(result, stringValue)
	}
	return result, nil
}

func schemaDefaultIntSlice(value any) ([]int, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, errors.Errorf("default value %v is not an array", value)
	}
	result := make([]int, 0, len(values))
	for _, item := range values {
		intValue, err := schemaDefaultInt(item)
		if err != nil {
			return nil, err
		}
		result = append(result, intValue)
	}
	return result, nil
}
