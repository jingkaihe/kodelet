package main

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/pkg/errors"
)

const maxStderrLength = 500

// agentExecutor runs a scheduled instruction as a new conversation through
// kodelet acp, on the schedule's runner and directory.
type agentExecutor struct {
	server  string
	command string
}

func (e agentExecutor) Execute(ctx context.Context, schedule Schedule, started func(conversationID string)) error {
	client := agent.NewClient(agent.ClientOptions{
		Server:  e.server,
		Runner:  schedule.Workspace,
		Command: e.command,
	})
	defer client.Close()
	session, err := client.CreateSession(ctx, agent.SessionOptions{
		CWD:     schedule.CWD,
		Profile: schedule.Profile,
	})
	if err != nil {
		return describeAgentError(err, "failed to start the conversation")
	}
	started(session.ID())
	response, err := session.RunAndWait(ctx, agent.RunOptions{Message: schedule.Instruction})
	if err != nil {
		return describeAgentError(err, "the run failed")
	}
	if response.StopReason == "cancelled" {
		return errors.New("the run was cancelled")
	}
	return nil
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
