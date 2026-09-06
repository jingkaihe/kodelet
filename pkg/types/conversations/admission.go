package conversations

import (
	"context"
	"time"
)

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

// ChildAdmission atomically reserves an ordinary durable turn while saving a
// new child or a compare-and-swapped resume. It is never supplied by a client.
type ChildAdmission struct {
	TurnAdmission
	ExpectedUpdatedAt time.Time // zero means insert-only
}

type childAdmissionKey struct{}

func ContextWithChildAdmission(ctx context.Context, admission ChildAdmission) context.Context {
	return context.WithValue(ctx, childAdmissionKey{}, admission)
}

func ChildAdmissionFromContext(ctx context.Context) (ChildAdmission, bool) {
	admission, ok := ctx.Value(childAdmissionKey{}).(ChildAdmission)
	return admission, ok
}
