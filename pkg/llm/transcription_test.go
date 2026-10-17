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
		name, provider, wantErr string
	}{
		{name: "OpenAI", provider: "openai"},
		{name: "normalized provider", provider: " OPENAI "},
		{name: "unsupported provider", provider: "anthropic", wantErr: "provider: openai"},
		{name: "no implicit provider", wantErr: "provider: openai"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transcriber, err := NewTranscriber(llmtypes.Config{Provider: test.provider}, "")
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
	t.Setenv("OPENAI_API_BASE", "")
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "unused-chat-model",
		Aliases:  map[string]string{"speech.v1": "speech-model", "speech-model": "wrong-model"},
		OpenAI:   &llmtypes.OpenAIConfig{BaseURL: server.URL},
	}
	before := config
	transcriber, err := NewTranscriber(config, " speech.v1 ")
	require.NoError(t, err)
	assert.Equal(t, before, config)
	_, err = transcriber.Transcribe(t.Context(), llmtypes.TranscriptionRequest{
		Audio: []byte("audio"), Filename: "clip.wav", ContentType: "audio/wav",
	})
	require.NoError(t, err)
}
