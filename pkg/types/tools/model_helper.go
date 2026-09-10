package tools

import (
	"context"
	"strings"

	"github.com/pkg/errors"
)

// ModelHelperWebFetchExtract is the only runner-delegated utility operation.
const ModelHelperWebFetchExtract = "web_fetch.extract"

// ModelHelperReadConversationExtract reads central history without runner delegation.
const ModelHelperReadConversationExtract = "read_conversation.extract"

// ModelHelperRequest supplies a document or a central conversation ID for an
// internal model utility. It accepts no configuration, paths, or persistence mode.
type ModelHelperRequest struct {
	Operation      string `json:"operation"`
	URL            string `json:"url"`
	Content        string `json:"content"`
	Prompt         string `json:"prompt"`
	ConversationID string `json:"conversationId,omitempty"`
}

// Validate rejects unsupported operations and bounds input below the RPC limit.
func (r ModelHelperRequest) Validate() error {
	switch r.Operation {
	case ModelHelperWebFetchExtract:
		if strings.TrimSpace(r.URL) == "" || strings.TrimSpace(r.Prompt) == "" {
			return errors.New("model helper URL and extraction prompt are required")
		}
		if r.ConversationID != "" {
			return errors.New("web extraction does not accept a conversation ID")
		}
	case ModelHelperReadConversationExtract:
		if strings.TrimSpace(r.ConversationID) == "" || strings.TrimSpace(r.Prompt) == "" {
			return errors.New("model helper conversation ID and extraction prompt are required")
		}
		if r.URL != "" || r.Content != "" {
			return errors.New("conversation extraction reads central history, not supplied URL or content")
		}
	default:
		return errors.New("unsupported internal model helper operation")
	}
	if len(r.URL) > 8192 ||
		len(r.ConversationID) > 8192 ||
		len(r.Prompt) > 64*1024 ||
		len(r.Content) > 512*1024 {
		return errors.New("model helper input exceeds the extraction limit")
	}
	return nil
}

// ModelHelper is an explicitly installed internal utility, not a client API.
type ModelHelper func(context.Context, ModelHelperRequest) (string, error)

type modelHelperContextKey struct{}

// ContextWithModelHelper installs the capability for one tool operation.
func ContextWithModelHelper(ctx context.Context, helper ModelHelper) context.Context {
	return context.WithValue(ctx, modelHelperContextKey{}, helper)
}

// ModelHelperFromContext returns the capability, if the tool's host installed it.
func ModelHelperFromContext(ctx context.Context) ModelHelper {
	helper, _ := ctx.Value(modelHelperContextKey{}).(ModelHelper)
	return helper
}

// RunModelHelper fails closed when no central capability is available.
func RunModelHelper(ctx context.Context, request ModelHelperRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := request.Validate(); err != nil {
		return "", err
	}
	helper := ModelHelperFromContext(ctx)
	if helper == nil {
		return "", errors.New("AI-assisted extraction is unavailable; use a daemon-managed run")
	}
	return helper(ctx, request)
}
