package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
