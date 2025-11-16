package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var mcpCallCmd = &cobra.Command{
	Use:   "call TOOL_NAME",
	Short: "Call an MCP tool directly from CLI",
	Long: `Call an MCP tool with specified arguments.

Tool name format: server-name.tool-name
Example: filesystem.read_file

Arguments should be provided as JSON using the --args flag.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		toolName := args[0]

		// Parse tool name (server.tool)
		parts := strings.Split(toolName, ".")
		if len(parts) != 2 {
			return errors.New("tool name must be in format: server-name.tool-name")
		}

		serverName := parts[0]
		toolShortName := parts[1]

		// Load MCP manager
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create MCP manager")
		}

		// Ensure cleanup with timeout to prevent hanging
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			done := make(chan struct{})
			go func() {
				if closeErr := mcpManager.Close(cleanupCtx); closeErr != nil {
					presenter.Warning(fmt.Sprintf("Failed to close MCP manager: %v", closeErr))
				}
				close(done)
			}()

			select {
			case <-done:
				// Cleanup completed successfully
			case <-cleanupCtx.Done():
				// Force exit if cleanup hangs - this is acceptable for CLI commands
				presenter.Warning("MCP cleanup timed out, forcing exit")
				os.Exit(0)
			}
		}()

		// Get flags
		argsJSON, _ := cmd.Flags().GetString("args")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		outputFile, _ := cmd.Flags().GetString("output")

		// Parse arguments
		var argsMap map[string]interface{}
		if err := json.Unmarshal([]byte(argsJSON), &argsMap); err != nil {
			return errors.Wrap(err, "invalid JSON arguments")
		}

		// Find and execute tool
		presenter.Info(fmt.Sprintf("Calling %s...", toolName))

		mcpTools, err := mcpManager.ListMCPTools(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to list MCP tools")
		}

		var targetTool *tools.MCPTool
		expectedToolName := fmt.Sprintf("mcp_%s_%s", serverName, toolShortName)
		for i, tool := range mcpTools {
			if tool.Name() == expectedToolName {
				targetTool = &mcpTools[i]
				break
			}
		}

		if targetTool == nil {
			return errors.Errorf("tool not found: %s (expected internal name: %s)", toolName, expectedToolName)
		}

		// Execute
		params, _ := json.Marshal(argsMap)
		result := targetTool.Execute(ctx, nil, string(params))

		if result.IsError() {
			presenter.Error(errors.New(result.GetError()), "Tool execution failed")
			return errors.New(result.GetError())
		}

		// Output result
		if outputFile != "" {
			if err := os.WriteFile(outputFile, []byte(result.GetResult()), 0o644); err != nil {
				return errors.Wrap(err, "failed to write output file")
			}
			presenter.Success(fmt.Sprintf("Output written to %s", outputFile))
		} else if jsonOutput {
			fmt.Println(result.GetResult())
		} else {
			presenter.Section("Result")
			fmt.Println(result.GetResult())
		}

		return nil
	},
}

func init() {
	mcpCallCmd.Flags().String("args", "{}", "JSON arguments for the tool")
	mcpCallCmd.Flags().Bool("json", false, "Output result as JSON only")
	mcpCallCmd.Flags().String("output", "", "Write output to file")
}
