package controlplane

import (
	"sync"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
)

type (
	ChatRequest        = chat.ChatRequest
	ChatContentBlock   = chat.ChatContentBlock
	ChatImageSource    = chat.ChatImageSource
	ChatImageURLSource = chat.ChatImageURLSource
	ChatEvent          = chat.ChatEvent
	UIInputEvent       = chat.UIInputEvent
	UIConfirmEvent     = chat.UIConfirmEvent
	UISelectEvent      = chat.UISelectEvent
	UINotifyEvent      = chat.UINotifyEvent
	ChatEventSink      = chat.ChatEventSink
)

var NewDefaultChatRunner = chat.NewDefaultChatRunner

type recordingChatSink struct {
	mu     sync.Mutex
	events []ChatEvent
}

func (s *recordingChatSink) Send(event ChatEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingChatSink) Events() []ChatEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChatEvent(nil), s.events...)
}
