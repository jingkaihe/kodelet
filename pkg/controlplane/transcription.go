package controlplane

import (
	"context"
	"io"
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
		return nil, errors.Wrap(err, "expected enabled and profile settings")
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
	// Use the literal profile key: names may contain dots, unlike Viper paths.
	rawModel := selected.Profiles[profile]["transcription_model"]
	model, ok := rawModel.(string)
	if rawModel != nil && !ok {
		return nil, errors.Errorf("transcription_model in profile %q must be a string", profile)
	}
	return llm.NewTranscriber(selected, model)
}

const maxTranscriptionBodyBytes = transcriptiontypes.MaxAudioBytes + (64 << 10)

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
		} else if errors.Is(err, transcriptiontypes.ErrUnsupportedAudio) {
			status = http.StatusUnsupportedMediaType
		}
		s.writeErrorResponse(w, status, err.Error(), nil)
		return
	}
	result, err := s.transcription.Transcribe(ctx, upload)
	if err != nil {
		if r.Context().Err() != nil {
			return // The caller disconnected; do not log speech or continue work.
		}
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, transcriptiontypes.ErrNoSpeech) {
			status = http.StatusUnprocessableEntity
		}
		// Errors are sanitized by the provider implementation. Never log
		// uploads, transcripts, or raw upstream response bodies here.
		s.writeErrorResponse(w, status, err.Error(), nil)
		return
	}
	s.writeJSONResponse(w, result)
}

func readTranscriptionUpload(r *http.Request) (transcriptiontypes.Request, error) {
	var upload transcriptiontypes.Request
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
			upload.Language = strings.TrimSpace(string(data))
			for _, c := range upload.Language {
				if c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
					return upload, errors.New("language must be a language code such as en")
				}
			}
			continue
		}
		if len(data) == 0 {
			return upload, errors.New("audio file is empty")
		}
		format, err := transcriptiontypes.DetectAudioFormat(data, part.Header.Get("Content-Type"))
		if err != nil {
			return upload, err
		}
		upload.Audio = data
		upload.ContentType = format.ContentType
	}
	if !seen["file"] {
		return upload, errors.New("audio file is required")
	}
	return upload, nil
}
