package controlplane

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/llm"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// newTranscriberFromViper resolves server enablement and the explicitly selected
// profile. Disabled transcription never loads or validates a model profile.
func newTranscriberFromViper() (llmtypes.Transcriber, error) {
	var config struct {
		Enabled bool   `mapstructure:"enabled"`
		Profile string `mapstructure:"profile"`
	}
	if err := viper.UnmarshalKey("transcription", &config); err != nil {
		return nil, errors.New("invalid transcription configuration; expected enabled and profile settings")
	}
	if !config.Enabled {
		return nil, nil //nolint:nilnil // Disabled transcription intentionally has no provider.
	}
	profile := strings.TrimSpace(config.Profile)
	if profile == "" {
		return nil, errors.New("transcription.profile is required when transcription is enabled")
	}
	selected, err := llm.GetConfigFromViperWithProfile(profile)
	if err != nil {
		return nil, errors.Wrap(err, "invalid transcription profile")
	}
	return llm.NewTranscriber(selected)
}

const maxTranscriptionBodyBytes = transcriptiontypes.MaxAudioBytes + (64 << 10)

var errUnsupportedAudio = errors.New("unsupported audio container or content type")

func (s *Server) handleTranscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	capabilities := transcriptiontypes.Capabilities{
		Reason:             "Transcription is disabled.",
		MaxAudioBytes:      transcriptiontypes.MaxAudioBytes,
		MaxDurationSeconds: transcriptiontypes.MaxDurationSeconds,
		MimeTypes:          transcriptiontypes.SupportedMimeTypes(),
	}
	if s.transcription != nil {
		capabilities.Enabled = true
		if err := s.transcription.CheckAvailability(); err != nil {
			capabilities.Reason = err.Error()
		} else {
			capabilities.Available = true
			capabilities.Reason = ""
		}
	}
	if r.Method == http.MethodGet {
		s.writeJSONResponse(w, capabilities)
		return
	}
	if !capabilities.Enabled || !capabilities.Available {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, capabilities.Reason, nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), transcriptiontypes.RequestTimeout)
	defer cancel()
	// Bound a stalled upload as well as the upstream call. HTTP/2 and HTTP/1
	// transports support this; test recorders need not implement deadlines.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(transcriptiontypes.RequestTimeout))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	r.Body = http.MaxBytesReader(w, r.Body, maxTranscriptionBodyBytes)
	defer r.Body.Close()
	upload, err := readTranscriptionUpload(r)
	if err != nil {
		status := http.StatusBadRequest
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			status = http.StatusRequestEntityTooLarge
		} else if errors.Is(err, errUnsupportedAudio) {
			status = http.StatusUnsupportedMediaType
		}
		s.writeErrorResponse(w, status, err.Error(), nil)
		return
	}
	result, err := s.transcription.Transcribe(ctx, llmtypes.TranscriptionRequest{
		Audio:       upload.audio,
		Filename:    upload.filename,
		ContentType: upload.contentType,
		Language:    upload.language,
	})
	if err != nil {
		if r.Context().Err() != nil {
			return // The caller disconnected; do not log speech or continue work.
		}
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, llmtypes.ErrNoSpeech) {
			status = http.StatusUnprocessableEntity
		}
		// Errors are sanitized by the provider implementation. Never log
		// uploads, transcripts, or raw upstream response bodies here.
		s.writeErrorResponse(w, status, err.Error(), nil)
		return
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		s.writeErrorResponse(w, http.StatusUnprocessableEntity, "No speech was recognized. Try recording again.", nil)
		return
	}
	s.writeJSONResponse(w, transcriptiontypes.Result{Text: text})
}

type transcriptionUpload struct {
	audio                           []byte
	filename, contentType, language string
}

func readTranscriptionUpload(r *http.Request) (transcriptionUpload, error) {
	var upload transcriptionUpload
	reader, err := r.MultipartReader()
	if err != nil {
		return upload, errors.New("expected multipart/form-data with one audio file")
	}
	seen := make(map[string]bool)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var limitErr *http.MaxBytesError
			if errors.As(err, &limitErr) {
				return upload, limitErr
			}
			return upload, errors.New("invalid multipart audio upload")
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "language") {
			return upload, errors.New("expected one file and at most one language field")
		}
		seen[name] = true
		limit := int64(64)
		if name == "file" {
			limit = transcriptiontypes.MaxAudioBytes
			if part.FileName() == "" {
				return upload, errors.New("audio file must have a filename")
			}
		} else if part.FileName() != "" {
			return upload, errors.New("language must be a text field")
		}
		data, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			var limitErr *http.MaxBytesError
			if errors.As(err, &limitErr) {
				return upload, limitErr
			}
			return upload, errors.New("failed to read audio upload")
		}
		if int64(len(data)) > limit {
			return upload, &http.MaxBytesError{Limit: limit}
		}
		if name == "language" {
			upload.language = strings.TrimSpace(string(data))
			for _, c := range upload.language {
				if c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
					return upload, errors.New("language must be a language code such as en")
				}
			}
			continue
		}
		if len(data) == 0 {
			return upload, errors.New("audio file is empty")
		}
		upload.audio = data
		upload.contentType, upload.filename, err = transcriptionAudioFormat(data, part.Header.Get("Content-Type"))
		if err != nil {
			return upload, err
		}
	}
	if !seen["file"] {
		return upload, errors.New("audio file is required")
	}
	return upload, nil
}

// Check container signatures rather than trusting a client filename. Providers
// perform full codec decoding; the control plane never invokes a media process.
func transcriptionAudioFormat(data []byte, declared string) (string, string, error) {
	var contentType, extension string
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		contentType, extension = "audio/wav", "wav"
	case bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		contentType, extension = "audio/webm", "webm"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		contentType, extension = "audio/mp4", "m4a"
	case bytes.HasPrefix(data, []byte("OggS")):
		contentType, extension = "audio/ogg", "ogg"
	case bytes.HasPrefix(data, []byte("fLaC")):
		contentType, extension = "audio/flac", "flac"
	case bytes.HasPrefix(data, []byte("ID3")) || len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0:
		contentType, extension = "audio/mpeg", "mp3"
	default:
		return "", "", errUnsupportedAudio
	}
	if declared != "" {
		mediaType, _, err := mime.ParseMediaType(declared)
		if err != nil {
			return "", "", errUnsupportedAudio
		}
		switch mediaType {
		case "audio/x-wav", "audio/wave":
			mediaType = "audio/wav"
		case "audio/x-m4a":
			mediaType = "audio/mp4"
		case "audio/mp3":
			mediaType = "audio/mpeg"
		}
		if mediaType != contentType && mediaType != "application/octet-stream" {
			return "", "", errUnsupportedAudio
		}
	}
	return contentType, "recording." + extension, nil
}
