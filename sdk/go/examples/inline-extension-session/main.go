// Command inline-extension-session runs an agent session whose sdk_echo tool
// is a callback in this process.
//
// Usage:
//
//	go run ./examples/inline-extension-session [message]
//
// Optional environment: KODELET_BIN, KODELET_SERVER, KODELET_RUNNER,
// KODELET_CWD, KODELET_PROFILE. Configure model credentials on the daemon.
// Press Ctrl+C to cancel the current run and close the session.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

type echoInput struct {
	Text string `json:"text"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("Usage: go run ./examples/inline-extension-session [message]")
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// Captured state lives in this process, not on the daemon or runner.
	var calls atomic.Int32
	echo := extension.New(extension.Metadata{Name: "inline-echo"})
	extension.RegisterTool(echo, extension.Tool[echoInput]{
		Name:        "sdk_echo",
		Description: "Echo text with a counter held in the Go application",
		Execute: func(ctx context.Context, in echoInput, tc *extension.ToolContext) (extension.ToolResult, error) {
			call := calls.Add(1)
			if err := tc.Update(ctx, fmt.Sprintf("Echo callback %d is running", call), nil); err != nil {
				return extension.ToolResult{}, err
			}
			if err := tc.UI.Notify(ctx, extension.NotifyRequest{Message: fmt.Sprintf("Callback %d: %s", call, in.Text)}); err != nil {
				return extension.ToolResult{}, err
			}
			return extension.Text(fmt.Sprintf("closure:%d: %s", call, in.Text)), nil
		},
	})

	cwd := os.Getenv("KODELET_CWD")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	client := agent.NewClient(agent.ClientOptions{
		Command: os.Getenv("KODELET_BIN"),
		Server:  os.Getenv("KODELET_SERVER"),
		Runner:  os.Getenv("KODELET_RUNNER"),
		CWD:     cwd,
	})
	// Closing the client also closes its sessions and callback runtimes.
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	session, err := client.CreateSession(ctx, agent.SessionOptions{
		Profile:    os.Getenv("KODELET_PROFILE"),
		Extensions: []*extension.Extension{echo},
		UI: &agent.UIHandlers{
			Notify: func(ctx context.Context, request extension.NotifyRequest) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "[SDK UI] %s\n", request.Message)
				return nil
			},
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Session: %s\n", session.ID())
	session.Subscribe(func(event agent.Event) {
		if update, ok := event.Data.(agent.ToolResult); ok && event.Type == agent.EventToolUpdate {
			fmt.Fprintln(os.Stderr, update.Result)
		}
	})

	message := strings.Join(os.Args[1:], " ")
	if message == "" {
		message = "Use sdk_echo twice to echo hello, then summarize the results."
	}
	response, err := session.RunAndWait(ctx, agent.RunOptions{Message: message})
	if err != nil {
		return err
	}
	fmt.Println(response.Content)
	fmt.Fprintf(os.Stderr, "Callbacks executed: %d\n", calls.Load())
	return nil
}
