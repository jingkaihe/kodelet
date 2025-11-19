package main

import (
	"encoding/json"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available MCP tools",
	Long: `List all available MCP tools from configured servers.

This command shows which MCP tools are accessible based on your configuration.
Use --detailed to see descriptions and --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		// Load MCP manager
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create MCP manager")
		}

		// Get flags
		serverFilter, _ := cmd.Flags().GetString("server")
		verbose, _ := cmd.Flags().GetBool("verbose")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		// List tools
		mcpTools, err := mcpManager.ListMCPTools(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to list MCP tools")
		}

		// Filter by server if specified
		if serverFilter != "" {
			filtered := []tools.MCPTool{}
			for _, tool := range mcpTools {
				if tool.ServerName() == serverFilter {
					filtered = append(filtered, tool)
				}
			}
			mcpTools = filtered
		}

		if jsonOutput {
			// JSON output
			data := make([]map[string]any, len(mcpTools))
			for i, tool := range mcpTools {
				data[i] = map[string]any{
					"name":        tool.Name(),
					"description": tool.Description(),
				}
				if verbose {
					data[i]["schema"] = tool.GenerateSchema()
				}
			}
			output, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return errors.Wrap(err, "failed to marshal JSON output")
			}
			fmt.Println(string(output))
		} else {
			// Human-readable output
			presenter.Section(fmt.Sprintf("Available MCP Tools (%d)", len(mcpTools)))

			// Group by server
			byServer := make(map[string][]tools.MCPTool)
			for _, tool := range mcpTools {
				serverName := tool.ServerName()
				byServer[serverName] = append(byServer[serverName], tool)
			}

			for serverName, serverTools := range byServer {
				fmt.Printf("\n%s (%d tools):\n", serverName, len(serverTools))
				for _, tool := range serverTools {
					toolName := tool.MCPToolName()
					fmt.Printf("  • %s.%s\n", serverName, toolName)
					if verbose {
						fmt.Printf("    %s\n", tool.Description())
					}
				}
			}
		}

		return nil
	},
}

func init() {
	mcpListCmd.Flags().String("server", "", "Filter by server name")
	mcpListCmd.Flags().BoolP("verbose", "v", false, "Show detailed tool information")
	mcpListCmd.Flags().Bool("json", false, "Output as JSON")
}
