package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	transcriptiontypes "github.com/jingkaihe/kodelet/pkg/types/transcription"
	"github.com/pkg/errors"
)

// TranscriptionCapabilities reports the selected server's dictation availability.
// Capture is client-owned; no runner or conversation is needed.
func (r *Client) TranscriptionCapabilities(ctx context.Context) (transcriptiontypes.Capabilities, error) {
	var result transcriptiontypes.Capabilities
	err := r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "transcription"}, nil, &result)
	return result, err
}

// TranscribeAudio uploads a completed recording and returns editable draft text.
// This never submits a chat turn, and never retries a possibly billed upload.
// Callers should discover capabilities and enforce recording duration locally.
func (r *Client) TranscribeAudio(ctx context.Context, audio []byte, filename, contentType, language string) (transcriptiontypes.Result, error) {
	var result transcriptiontypes.Result
	if r == nil || r.client == nil {
		return result, errors.New("the chat connection is not initialized")
	}
	if len(audio) == 0 || int64(len(audio)) > transcriptiontypes.MaxAudioBytes {
		return result, errors.New("recording must contain audio and be no larger than 20 MiB")
	}
	if filename == "" {
		return result, errors.New("recording filename is required")
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return result, errors.New("recording content type is invalid")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return result, errors.Wrap(err, "failed to create audio upload")
	}
	if _, err := part.Write(audio); err != nil {
		return result, errors.Wrap(err, "failed to write audio upload")
	}
	if language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return result, errors.Wrap(err, "failed to write transcription language")
		}
	}
	if err := writer.Close(); err != nil {
		return result, errors.Wrap(err, "failed to finish audio upload")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "transcription")
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, transcriptiontypes.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return result, errors.Wrap(err, "failed to create transcription request")
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "transcription request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, transcriptiontypes.MaxResponseBytes+1))
	if err != nil {
		return result, errors.Wrap(err, "failed to read transcription response")
	}
	if int64(len(payload)) > transcriptiontypes.MaxResponseBytes {
		return result, errors.New("transcription response exceeds the size limit")
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return result, errors.Wrap(err, "invalid transcription response")
	}
	result.Text = strings.TrimSpace(result.Text)
	if result.Text == "" {
		return result, errors.New("no speech was recognized")
	}
	return result, nil
}
