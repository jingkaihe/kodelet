package llm

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestNewAgentRunOutcome(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		reason AgentRunReason
		want   AgentRunOutcome
	}{
		{name: "completed", want: AgentRunOutcome{Status: "succeeded", Reason: "completed"}},
		{name: "turn limit", reason: "turn_limit", want: AgentRunOutcome{Status: "succeeded", Reason: "turn_limit"}},
		{name: "error", err: errors.New("provider failed"), want: AgentRunOutcome{Status: "failed", Reason: "error", Error: "provider failed"}},
		{name: "canceled", err: errors.Wrap(context.Canceled, "request"), want: AgentRunOutcome{Status: "canceled", Reason: "canceled", Error: "request: context canceled"}},
		{name: "deadline overrides limit", err: context.DeadlineExceeded, reason: "turn_limit", want: AgentRunOutcome{Status: "canceled", Reason: "deadline_exceeded", Error: "context deadline exceeded"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, NewAgentRunOutcome(test.err, test.reason))
		})
	}
}

func TestMessageOptResolvedInitiator(t *testing.T) {
	assert.Equal(t, InitiatorUser, MessageOpt{}.ResolvedInitiator())
	assert.Equal(t, InitiatorAgent, MessageOpt{Initiator: " agent "}.ResolvedInitiator())
	assert.Equal(t, InitiatorUser, MessageOpt{Initiator: " USER "}.ResolvedInitiator())
}

func TestMessageOptWithTurnInitiator(t *testing.T) {
	assert.Equal(t, InitiatorUser, MessageOpt{}.WithTurnInitiator(0).Initiator)
	assert.Equal(t, InitiatorAgent, MessageOpt{}.WithTurnInitiator(1).Initiator)
	assert.Equal(t, InitiatorAgent, MessageOpt{Initiator: InitiatorAgent}.WithTurnInitiator(0).Initiator)
}

func TestMessageOptWithInitiator(t *testing.T) {
	original := MessageOpt{NoToolUse: true}
	updated := original.WithInitiator(InitiatorAgent)

	assert.Empty(t, original.Initiator)
	assert.Equal(t, InitiatorAgent, updated.Initiator)
	assert.True(t, updated.NoToolUse)
}
