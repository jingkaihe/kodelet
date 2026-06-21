package webui

type recordingChatSink struct {
	events []ChatEvent
}

func (s *recordingChatSink) Send(event ChatEvent) error {
	s.events = append(s.events, event)
	return nil
}
