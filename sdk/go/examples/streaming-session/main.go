// Command streaming-session streams assistant text and tool activity.
//
//	go run ./examples/streaming-session "List the Go packages in this repository"
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
)

func main() {
	message := strings.Join(os.Args[1:], " ")
	if message == "" {
		message = "Describe the layout of the current directory."
	}
	client := agent.NewClient(agent.ClientOptions{Command: os.Getenv("KODELET_BIN")})
	defer client.Close()

	// Ctrl+C cancels the run; Kodelet stops the turn and the stream ends.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	session, err := client.CreateSession(ctx, agent.SessionOptions{Profile: os.Getenv("KODELET_PROFILE")})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for event, err := range session.Stream(ctx, agent.RunOptions{Message: message}) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nerror:", err)
			os.Exit(1)
		}
		switch data := event.Data.(type) {
		case agent.AssistantMessageDelta:
			fmt.Print(data.DeltaContent)
		case agent.ToolCall:
			fmt.Fprintf(os.Stderr, "\n[tool] %s %s\n", data.ToolName, data.RawInput)
		case agent.ToolResult:
			if event.Type == agent.EventToolResult {
				fmt.Fprintf(os.Stderr, "[tool] %s %s\n", data.ToolName, data.Status)
			}
		}
	}
	fmt.Println()
}
