package main

import (
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/mcp/codegen"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var mcpGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate TypeScript API files for MCP tools",
	Long: `Generate TypeScript API files for configured MCP tools.

This creates a filesystem representation of your MCP tools that can be:
- Called directly using Node.js with tsx
- Used by the code execution environment
- Inspected to understand available tools`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		// Load MCP configuration
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create MCP manager")
		}

		// Get flags
		outputDir, _ := cmd.Flags().GetString("output")
		serverFilter, _ := cmd.Flags().GetString("server")
		clean, _ := cmd.Flags().GetBool("clean")

		// Clean if requested
		if clean {
			presenter.Info("Cleaning existing generated files...")
			if err := os.RemoveAll(outputDir); err != nil {
				return errors.Wrap(err, "failed to clean output directory")
			}
		}

		// Generate
		presenter.Info("Generating TypeScript API files...")
		generator := codegen.NewMCPCodeGenerator(mcpManager, outputDir)
		if serverFilter != "" {
			generator.SetServerFilter(serverFilter)
		}

		if err := generator.Generate(ctx); err != nil {
			return errors.Wrap(err, "failed to generate code")
		}

		// Count generated files
		stats := generator.GetStats()
		presenter.Success(fmt.Sprintf("Generated %d tools from %d servers",
			stats.ToolCount, stats.ServerCount))
		presenter.Info(fmt.Sprintf("Output directory: %s", outputDir))

		// Show example usage
		presenter.Section("Example Usage")
		fmt.Printf(`
You can now call MCP tools directly using Node.js:

    npx tsx %s/example.ts

Or explore the generated API:

    ls %s/servers/
    cat %s/servers/*/index.ts
`, outputDir, outputDir, outputDir)

		return nil
	},
}

func init() {
	mcpGenerateCmd.Flags().String("output", ".kodelet/mcp", "Output directory for generated files")
	mcpGenerateCmd.Flags().String("server", "", "Generate only for specific server")
	mcpGenerateCmd.Flags().Bool("clean", false, "Clean output directory before generating")
}
