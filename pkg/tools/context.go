package tools

import (
	"context"
	"strings"
)

type conversationIDContextKey struct{}

func ContextWithConversationID(ctx context.Context, conversationID string) context.Context {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ctx
	}
	return context.WithValue(ctx, conversationIDContextKey{}, conversationID)
}

func conversationIDFromContext(ctx context.Context) string {
	conversationID, _ := ctx.Value(conversationIDContextKey{}).(string)
	return strings.TrimSpace(conversationID)
}
