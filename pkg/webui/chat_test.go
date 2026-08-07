package webui

import "sync"

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
