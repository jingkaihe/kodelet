package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelHelperRequestValidate(t *testing.T) {
	valid := ModelHelperRequest{Operation: ModelHelperWebFetchExtract, URL: "https://example.com", Content: "document", Prompt: "Extract the title"}
	tests := []struct {
		name   string
		change func(*ModelHelperRequest)
		err    string
	}{
		{name: "valid", change: func(*ModelHelperRequest) {}},
		{name: "empty content", change: func(r *ModelHelperRequest) { r.Content = "" }},
		{name: "missing operation", change: func(r *ModelHelperRequest) { r.Operation = "" }, err: "unsupported"},
		{name: "other operation", change: func(r *ModelHelperRequest) { r.Operation = "conversation.delete" }, err: "unsupported"},
		{name: "missing URL", change: func(r *ModelHelperRequest) { r.URL = " \t" }, err: "required"},
		{name: "missing prompt", change: func(r *ModelHelperRequest) { r.Prompt = "\n " }, err: "required"},
		{name: "URL limit", change: func(r *ModelHelperRequest) { r.URL = strings.Repeat("u", 8192) }},
		{name: "prompt limit", change: func(r *ModelHelperRequest) { r.Prompt = strings.Repeat("p", 64*1024) }},
		{name: "content limit", change: func(r *ModelHelperRequest) { r.Content = strings.Repeat("c", 512*1024) }},
		{name: "oversized URL", change: func(r *ModelHelperRequest) { r.URL = strings.Repeat("u", 8193) }, err: "limit"},
		{name: "oversized prompt", change: func(r *ModelHelperRequest) { r.Prompt = strings.Repeat("p", 64*1024+1) }, err: "limit"},
		{name: "oversized content", change: func(r *ModelHelperRequest) { r.Content = strings.Repeat("c", 512*1024+1) }, err: "limit"},
		{name: "limit counts bytes", change: func(r *ModelHelperRequest) { r.Prompt = strings.Repeat("é", 32*1024+1) }, err: "limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := valid
			tt.change(&request)
			err := request.Validate()
			if tt.err != "" {
				assert.ErrorContains(t, err, tt.err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRunModelHelper(t *testing.T) {
	request := ModelHelperRequest{Operation: ModelHelperWebFetchExtract, URL: "https://example.com", Content: "document", Prompt: "Extract the title"}
	helperErr := errors.New("central provider unavailable")
	tests := []struct {
		name      string
		absent    bool
		masked    bool
		canceled  bool
		invalid   bool
		helperErr error
		wantCalls int
		wantErr   string
	}{
		{name: "delegates", wantCalls: 1},
		{name: "propagates error", helperErr: helperErr, wantCalls: 1, wantErr: helperErr.Error()},
		{name: "absent capability", absent: true, wantErr: "central model helper is unavailable"},
		{name: "nil masks inherited capability", masked: true, wantErr: "central model helper is unavailable"},
		{name: "canceled before invocation", canceled: true, wantErr: context.Canceled.Error()},
		{name: "invalid before invocation", invalid: true, wantErr: "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			if !tt.absent {
				ctx = ContextWithModelHelper(ctx, func(helperCtx context.Context, got ModelHelperRequest) (string, error) {
					calls++
					assert.Same(t, ctx, helperCtx)
					assert.Equal(t, request, got)
					if tt.helperErr != nil {
						return "", tt.helperErr
					}
					return "extracted", nil
				})
			}
			if tt.masked {
				ctx = ContextWithModelHelper(ctx, nil)
			}
			if tt.absent || tt.masked {
				assert.Nil(t, ModelHelperFromContext(ctx))
			} else {
				assert.NotNil(t, ModelHelperFromContext(ctx))
			}
			if tt.canceled {
				cancel()
			}
			input := request
			if tt.invalid {
				input.Operation = "agent.run"
			}
			text, err := RunModelHelper(ctx, input)
			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, text)
				if tt.helperErr != nil {
					assert.ErrorIs(t, err, tt.helperErr)
				}
				if tt.canceled {
					assert.ErrorIs(t, err, context.Canceled)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, "extracted", text)
			}
		})
	}
}
