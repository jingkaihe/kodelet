package main

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/pkg/errors"
)

const maxStderrLength = 500

// errRunCancelled reports a run whose turn was stopped from outside the
// daemon, such as from the Web UI.
var errRunCancelled = errors.New("the run was cancelled")

// agentExecutor runs a scheduled instruction as a new conversation through
// kodelet acp, on the schedule's runner, directory, and profile. It also reads
// and stops those conversations on the server.
type agentExecutor struct {
	server  string
	command string
}

// Execute runs the instruction and reports the agent's final output and run
// stats, including for runs that fail after the conversation starts. Closing
// detach abandons the run: kodelet acp exits, and the conversation keeps
// running on the server.
func (e agentExecutor) Execute(ctx context.Context, schedule Schedule, started func(conversationID string), detach <-chan struct{}) (runResult, error) {
	client := agent.NewClient(agent.ClientOptions{
		Server:  e.server,
		Runner:  schedule.Workspace,
		Command: e.command,
	})
	defer client.Close()
	// Cancelling a run's context cancels its turn, so detaching closes the
	// client instead. Only opening the session, before any turn exists,
	// follows detach.
	startCtx, cancelStart := context.WithCancel(ctx)
	defer cancelStart()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-detach:
			cancelStart()
			_ = client.Close()
		case <-done:
		}
	}()
	session, err := client.CreateSession(startCtx, agent.SessionOptions{
		CWD:     schedule.CWD,
		Profile: schedule.Profile,
	})
	if err != nil {
		return runResult{}, describeAgentError(err, "failed to start the conversation")
	}
	started(session.ID())
	collector := newRunCollector()
	unsubscribe := session.Subscribe(collector.observe)
	response, runErr := session.RunAndWait(ctx, agent.RunOptions{Message: schedule.Instruction})
	unsubscribe()
	result := collector.result()
	if result.Output == "" && response != nil {
		result.Output = strings.TrimSpace(response.Content)
	}
	switch {
	case runErr != nil:
		return result, describeAgentError(runErr, "the run failed")
	case response.StopReason == "cancelled":
		return result, errRunCancelled
	default:
		return result, nil
	}
}

// Status reads a conversation's status from the server.
func (e agentExecutor) Status(ctx context.Context, conversationID string) (agent.ConversationStatus, error) {
	return e.conversationClient().GetConversationStatus(ctx, conversationID)
}

// Stop stops a conversation's turn on the server.
func (e agentExecutor) Stop(ctx context.Context, conversationID, turnID string) error {
	return e.conversationClient().StopConversation(ctx, conversationID, agent.StopConversationOptions{TurnID: turnID})
}

func (e agentExecutor) conversationClient() *agent.Client {
	return agent.NewClient(agent.ClientOptions{Server: e.server, Command: e.command})
}

// describeAgentError adds the last stderr line of an exited kodelet acp
// process, which usually explains the failure, such as an unknown runner.
func describeAgentError(err error, message string) error {
	var runErr *agent.RunError
	if errors.As(err, &runErr) {
		if line := lastLine(runErr.Stderr); line != "" {
			return errors.Errorf("%s: %s: %s", message, runErr.Message, truncate(line, maxStderrLength))
		}
	}
	return errors.Wrap(err, message)
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
