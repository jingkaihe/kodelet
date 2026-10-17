package llm

import (
	"context"
	"time"

	"github.com/pkg/errors"
)

const (
	// MaxTranscriptionAudioBytes bounds a recording, excluding upload framing.
	MaxTranscriptionAudioBytes int64 = 20 << 20
	// TranscriptionRequestTimeout bounds provider work, including authentication.
	TranscriptionRequestTimeout = 90 * time.Second
	// MaxTranscriptionResponseBytes bounds responses read from a provider.
	MaxTranscriptionResponseBytes int64 = 1 << 20
)

// ErrNoSpeech means the provider successfully returned an empty transcript.
var ErrNoSpeech = errors.New("no speech was recognized in the recording")

// TranscriptionRequest contains one recording and an optional language hint.
type TranscriptionRequest struct {
	Audio       []byte
	Filename    string
	ContentType string
	Language    string
}

// TranscriptionResult is recognized text, independent of any conversation.
type TranscriptionResult struct {
	Text string
}

// Transcriber recognizes speech without creating a chat thread or agent turn.
// Errors must be safe to display without exposing credentials or provider bodies.
type Transcriber interface {
	// CheckAvailability checks local credentials without network access or refresh.
	// A nil error does not guarantee upstream access; requests resolve credentials again.
	CheckAvailability() error
	Transcribe(context.Context, TranscriptionRequest) (TranscriptionResult, error)
}

// SupportedTranscriptionMimeTypes returns accepted recording container types.
func SupportedTranscriptionMimeTypes() []string {
	return []string{"audio/webm", "audio/mp4", "audio/wav", "audio/mpeg", "audio/ogg", "audio/flac"}
}
