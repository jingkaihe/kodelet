package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTranscriber(t *testing.T) {
	for _, test := range []struct {
		name, provider, platform, wantErr string
	}{
		{name: "OpenAI", provider: "openai"},
		{name: "normalized provider", provider: " OPENAI "},
		{name: "Codex", provider: "openai", platform: "codex"},
		{name: "unsupported provider", provider: "anthropic", wantErr: "provider: openai"},
		{name: "no implicit provider", wantErr: "provider: openai"},
		{name: "unsupported platform", provider: "openai", platform: "copilot", wantErr: "supports only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := llmtypes.Config{Provider: test.provider}
			if test.platform != "" {
				config.OpenAI = &llmtypes.OpenAIConfig{Platform: test.platform}
			}
			transcriber, err := NewTranscriber(config)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.IsType(t, &openai.Transcriber{}, transcriber)
		})
	}
}

func TestNewTranscriberResolvesModelAliasesOnce(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "unresolved", true: "resolved"}[resolved], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				assert.Equal(t, "speech-model", r.FormValue("model"))
				_, _ = io.WriteString(w, `{"text":"Recognized words."}`)
			}))
			t.Cleanup(server.Close)
			t.Setenv("OPENAI_API_KEY", "test-key")
			config := llmtypes.Config{
				Provider:             "openai",
				Model:                "unused-chat-model",
				TranscriptionModel:   " speech.v1 ",
				Aliases:              map[string]string{"speech.v1": "speech-model", "speech-model": "wrong-model"},
				ModelAliasesResolved: resolved,
				OpenAI:               &llmtypes.OpenAIConfig{BaseURL: server.URL},
			}
			if resolved {
				config.TranscriptionModel = "speech-model"
			}
			before := config
			transcriber, err := NewTranscriber(config)
			require.NoError(t, err)
			assert.Equal(t, before, config)
			result, err := transcriber.Transcribe(t.Context(), llmtypes.TranscriptionRequest{
				Audio: []byte("audio"), Filename: "clip.wav", ContentType: "audio/wav",
			})
			require.NoError(t, err)
			assert.Equal(t, "Recognized words.", result.Text)
		})
	}
}
