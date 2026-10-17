// Package transcription defines the client-independent microphone dictation protocol.
package transcription

import llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"

const (
	// Path supports GET capabilities and POST multipart audio, without a conversation or runner.
	Path = "/api/transcription"
	// MaxAudioBytes bounds the audio file, excluding multipart framing.
	MaxAudioBytes = llmtypes.MaxTranscriptionAudioBytes
	// MaxDurationSeconds is the recording limit clients must enforce before upload.
	MaxDurationSeconds = 120
	// RequestTimeout bounds transcription requests, including upload and provider work.
	RequestTimeout = llmtypes.TranscriptionRequestTimeout
	// MaxResponseBytes bounds text and error responses consumed by clients.
	MaxResponseBytes = llmtypes.MaxTranscriptionResponseBytes
)

// Capabilities describes server-side availability, not local microphone permission.
// Clients must also check their capture implementation and enforce the advertised limits.
type Capabilities struct {
	Enabled            bool     `json:"enabled"`
	Available          bool     `json:"available"`
	Reason             string   `json:"reason,omitempty"`
	MaxAudioBytes      int64    `json:"maxAudioBytes"`
	MaxDurationSeconds int      `json:"maxDurationSeconds"`
	MimeTypes          []string `json:"mimeTypes"`
}

// Result is draft text only. Transcription never submits a turn or saves a message.
type Result struct {
	Text string `json:"text"`
}

// SupportedMimeTypes returns container types accepted by the upload protocol.
// Parameters such as codecs=opus are allowed; browser and native clients may differ.
func SupportedMimeTypes() []string {
	return llmtypes.SupportedTranscriptionMimeTypes()
}
