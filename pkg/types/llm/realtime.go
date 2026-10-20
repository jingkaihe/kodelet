package llm

import "context"

// RealtimeProvider negotiates browser/native WebRTC media and owns the provider
// sideband. Credentials and delegation handling never belong to the media client.
type RealtimeProvider interface {
	CheckAvailability() error
	VoiceOptions() (defaultVoice string, voices []string)
	Open(context.Context, RealtimeOffer) (RealtimeSession, error)
}

// RealtimeOffer supplies a WebRTC offer and the host's bounded session context.
type RealtimeOffer struct {
	SDP          string
	Instructions string
	Voice        string // Empty uses the provider's configured default.
}

// RealtimeUpdate contains incremental assistant text from a delegated task.
// BlockEnd ends an assistant text block, not the delegated turn.
type RealtimeUpdate struct {
	Text     string
	BlockEnd bool
}

// RealtimeSession is independent of coding turns. Close ends voice, not work.
// Open's context owns the session lifetime; Events closes when the session ends.
// The host serializes Update and Complete calls, batching deltas or block ends.
type RealtimeSession interface {
	Answer() string
	Events() <-chan RealtimeEvent
	Update(ctx context.Context, handoffID string, update RealtimeUpdate) error
	// Complete with an empty result resolves a secondary handoff without speech.
	Complete(ctx context.Context, handoffID, result string) error
	Close() error
}

// RealtimeEvent is provider-neutral. Transcript Text is cumulative, ID is stable
// within an utterance, and Final seals it. Only delegate events request work;
// transcripts and provider errors are never execution instructions.
// Kinds are transcript, delegate, speech_started, error and closed.
// A delegate always has finalized task Text and provider HandoffID; ItemID is
// optional and identifies the associated user utterance, never a function item.
type RealtimeEvent struct {
	Kind      string
	ID        string
	Role      string
	Text      string
	Final     bool
	HandoffID string
	ItemID    string
}
