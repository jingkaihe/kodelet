package main

import (
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage and interact with MCP (Model Context Protocol) tools",
	Long: `Commands for working with MCP servers and tools.

MCP provides a standard way to connect AI agents to external systems.
These commands help you manage MCP servers, generate code, and call tools.`,
}

func init() {
	mcpCmd.AddCommand(mcpGenerateCmd)
	mcpCmd.AddCommand(mcpListCmd)
	mcpCmd.AddCommand(mcpCallCmd)
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
