package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sirupsen/logrus"
)

func main() {
	ctx := context.Background()
	pwd, err := os.Getwd()
	if err != nil {
		logrus.WithError(err).Fatal("failed to get current working directory")
	}
	mcpServersConfig := tools.MCPServersConfig{
		Servers: map[string]tools.MCPServerConfig{
			"server-filesystem": {
				ServerType:    tools.MCPServerTypeStdio,
				Command:       "npx",
				Args:          []string{"-y", "@modelcontextprotocol/server-filesystem", pwd},
				ToolWhiteList: []string{"list_directory"},
			},
		},
	}
	mcpManager, err := tools.NewMCPManager(mcpServersConfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create mcp manager")
	}
	err = mcpManager.Initialize(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("failed to initialize mcp manager")
	}
	defer mcpManager.Close(ctx)
	toolResult, err := mcpManager.ListMCPTools(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("failed to list mcp tools")
	}

	tool := toolResult[0]

	input := map[string]any{
		"path": pwd,
	}
	params, err := json.Marshal(input)
	if err != nil {
		logrus.WithError(err).Fatal("failed to marshal input")
	}
	result := tool.Execute(ctx, tools.NewBasicState(), string(params))
	fmt.Println(result.String())
}
