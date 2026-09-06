package conversations

import "context"

// TurnAdmission fences an ordinary conversation checkpoint to a durable running
// receipt and its exact reserved runner run. Only central orchestration sets it.
type TurnAdmission struct {
	ConversationID     string
	RunID              string
	RunnerID           string
	EnvironmentProfile string
}

type turnAdmissionKey struct{}

// ContextWithTurnAdmission asks the store to atomically save history and affinity.
func ContextWithTurnAdmission(ctx context.Context, admission TurnAdmission) context.Context {
	return context.WithValue(ctx, turnAdmissionKey{}, admission)
}

// TurnAdmissionFromContext returns the checkpoint's exact admission fence.
func TurnAdmissionFromContext(ctx context.Context) (TurnAdmission, bool) {
	admission, ok := ctx.Value(turnAdmissionKey{}).(TurnAdmission)
	return admission, ok
}
