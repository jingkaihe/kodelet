// Package transcription defines the client-independent microphone dictation protocol.
package transcription

import (
	"bytes"
	"mime"
	"slices"
	"time"

	"github.com/pkg/errors"
)

const (
	// Path supports GET capabilities and POST multipart audio, without a conversation or runner.
	Path = "/api/transcription"
	// MaxAudioBytes bounds the audio file, excluding multipart framing.
	MaxAudioBytes int64 = 20 << 20
	// MaxDurationSeconds is the recording limit clients must enforce before upload.
	MaxDurationSeconds = 120
	// RequestTimeout bounds transcription requests, including upload and provider work.
	RequestTimeout = 90 * time.Second
	// MaxResponseBytes bounds transcription responses read from providers and servers.
	MaxResponseBytes int64 = 1 << 20
)

// ErrNoSpeech means the provider successfully returned an empty transcript.
var ErrNoSpeech = errors.New("no speech was recognized in the recording")

// ErrUnsupportedAudio indicates an unknown container or a mismatched content type.
var ErrUnsupportedAudio = errors.New("unsupported audio container or content type")

// Request contains a recording validated by the control plane. Audio must be
// nonempty and at most MaxAudioBytes, with a matching canonical ContentType.
// Language must be empty or a trimmed code of at most 64 ASCII letters/hyphens.
type Request struct {
	Audio       []byte
	ContentType string
	Language    string
}

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
	types := make([]string, len(audioFormats))
	for i, format := range audioFormats {
		types[i] = format.ContentType
	}
	return types
}

// AudioFormat identifies a recording container and its fixed provider filename.
type AudioFormat struct {
	ContentType string
	Filename    string
	aliases     []string
	match       func([]byte) bool
}

var audioFormats = []AudioFormat{
	{
		ContentType: "audio/webm", Filename: "recording.webm",
		match: func(data []byte) bool { return bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3}) },
	},
	{
		ContentType: "audio/mp4", Filename: "recording.m4a", aliases: []string{"audio/x-m4a"},
		match: func(data []byte) bool { return len(data) >= 12 && string(data[4:8]) == "ftyp" },
	},
	{
		ContentType: "audio/wav", Filename: "recording.wav", aliases: []string{"audio/x-wav", "audio/wave"},
		match: func(data []byte) bool {
			return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE"
		},
	},
	{
		ContentType: "audio/mpeg", Filename: "recording.mp3", aliases: []string{"audio/mp3"},
		match: func(data []byte) bool {
			return bytes.HasPrefix(data, []byte("ID3")) || len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0
		},
	},
	{
		ContentType: "audio/ogg", Filename: "recording.ogg",
		match: func(data []byte) bool { return bytes.HasPrefix(data, []byte("OggS")) },
	},
	{
		ContentType: "audio/flac", Filename: "recording.flac",
		match: func(data []byte) bool { return bytes.HasPrefix(data, []byte("fLaC")) },
	},
}

// LookupAudioFormat returns a format by its canonical MIME type.
func LookupAudioFormat(contentType string) (AudioFormat, error) {
	for _, format := range audioFormats {
		if contentType == format.ContentType {
			return format, nil
		}
	}
	return AudioFormat{}, ErrUnsupportedAudio
}

// DetectAudioFormat checks container signatures and the declared MIME type.
// Full codec decoding remains the provider's responsibility.
func DetectAudioFormat(data []byte, declared string) (AudioFormat, error) {
	mediaType := ""
	if declared != "" {
		var err error
		mediaType, _, err = mime.ParseMediaType(declared)
		if err != nil {
			return AudioFormat{}, ErrUnsupportedAudio
		}
	}
	for _, format := range audioFormats {
		if format.match(data) {
			if mediaType == "" || mediaType == "application/octet-stream" ||
				mediaType == format.ContentType || slices.Contains(format.aliases, mediaType) {
				return format, nil
			}
			break
		}
	}
	return AudioFormat{}, ErrUnsupportedAudio
}
