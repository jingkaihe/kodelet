package extension

import "errors"

const conversationForkUnavailableCode = -32004

// ErrConversationForkUnavailable matches errors returned when live
// conversation forking is unsupported or unavailable for the current call.
var ErrConversationForkUnavailable = errors.New("live conversation forking is unavailable")

// ErrRuntimeClosed is returned when a closed runtime receives a message or a
// handler issues a host request after the runtime closed.
var ErrRuntimeClosed = errors.New("extension runtime is closed")

// ErrRequestInactive is returned for host requests scoped to a call that has
// already completed or been cancelled.
var ErrRequestInactive = errors.New("extension request is no longer active")

// ForkUnavailableError reports why a conversation could not be forked.
type ForkUnavailableError struct {
	Message string
}

func (e *ForkUnavailableError) Error() string {
	return e.Message
}

// Is matches ErrConversationForkUnavailable.
func (e *ForkUnavailableError) Is(target error) bool {
	return target == ErrConversationForkUnavailable
}
