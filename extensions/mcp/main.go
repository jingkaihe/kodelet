// kodelet-extension-mcp exposes MCP servers through Kodelet's extension protocol.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/pkg/errors"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("kodelet-extension-mcp", version)
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("MCP extension failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cwd, err := workspaceCWD()
	if err != nil {
		return errors.Wrap(err, "resolve workspace")
	}
	config, err := loadConfig(cwd)
	if err != nil {
		return err
	}
	ext := extension.New(extension.Metadata{Name: "kodelet-mcp", Version: version})
	closeServers := registerMCP(ctx, ext, config, cwd, serverStartupTimeout)
	defer closeServers()
	return ext.Run(ctx)
}
