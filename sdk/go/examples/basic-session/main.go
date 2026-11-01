// Command basic-session sends one message to a Kodelet agent and prints the
// answer.
//
//	go run ./examples/basic-session "What does this repository do?"
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
)

func main() {
	message := strings.Join(os.Args[1:], " ")
	if message == "" {
		message = "What is the meaning of life?"
	}
	client := agent.NewClient(agent.ClientOptions{Command: os.Getenv("KODELET_BIN")})
	defer client.Close()

	ctx := context.Background()
	session, err := client.CreateSession(ctx, agent.SessionOptions{
		Options: agent.ExecutionOptions{MaxTurns: agent.Ptr(5)},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	response, err := session.RunAndWait(ctx, agent.RunOptions{Message: message})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(response.Content)
	fmt.Fprintf(os.Stderr, "Conversation: %s\n", response.ConversationID)
}
