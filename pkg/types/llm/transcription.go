package llm

import (
	"context"

	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
)

// Transcriber recognizes speech without creating a chat thread or agent turn.
// Errors must be safe to display without exposing credentials or provider bodies.
type Transcriber interface {
	// CheckAvailability checks local credentials without network access or refresh.
	// A nil error does not guarantee upstream access; requests resolve credentials again.
	CheckAvailability() error
	// Transcribe returns normalized, nonempty text on success, or
	// transcriptiontypes.ErrNoSpeech when no speech was recognized.
	// The caller owns the deadline; cancellation covers authentication and upload.
	Transcribe(context.Context, transcriptiontypes.Request) (transcriptiontypes.Result, error)
}
