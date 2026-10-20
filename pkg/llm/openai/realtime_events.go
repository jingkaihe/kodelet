package openai

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

type realtimeWireItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Target    string `json:"target"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type realtimeWireEvent struct {
	Type       string           `json:"type"`
	ItemID     string           `json:"item_id"`
	Delta      string           `json:"delta"`
	Transcript string           `json:"transcript"`
	Text       string           `json:"text"`
	Name       string           `json:"name"`
	CallID     string           `json:"call_id"`
	Arguments  string           `json:"arguments"`
	Item       realtimeWireItem `json:"item"`
	Turn       struct {
		ID         string `json:"id"`
		Role       string `json:"role"`
		Transcript string `json:"transcript"`
	} `json:"turn"`
	Error json.RawMessage `json:"error"`
}

func (s *realtimeSession) handleLocked(event realtimeWireEvent) ([]llmtypes.RealtimeEvent, error) {
	if event.Type == "error" {
		var detail struct{ Code string }
		_ = json.Unmarshal(event.Error, &detail)
		if s.provider.platform != "codex" && detail.Code == "conversation_already_has_active_response" {
			s.responseActive, s.responsePending = true, true
			return nil, nil
		}
		// Provider error messages can contain credentials, prompts, or SDP.
		return nil, errors.New("realtime provider reported an error")
	}
	if s.provider.platform == "codex" {
		return s.handleNativeLocked(event)
	}
	switch event.Type {
	case "conversation.item.input_audio_transcription.delta":
		return s.transcriptLocked(event.ItemID, "user", event.Delta, false)
	case "conversation.item.input_audio_transcription.completed":
		return s.transcriptLocked(event.ItemID, "user", event.Transcript, true)
	case "conversation.item.input_audio_transcription.failed":
		// Captions are presentation only: the model heard the audio itself.
		// Seal what was transcribed, or mark the gap, rather than hanging up.
		if event.ItemID == "" {
			return nil, nil
		}
		text := s.transcripts[event.ItemID].text
		if strings.TrimSpace(text) == "" {
			text = "(Speech could not be transcribed.)"
		}
		return s.transcriptLocked(event.ItemID, "user", text, true)
	case "response.output_audio_transcript.delta", "response.audio_transcript.delta", "response.output_text.delta":
		return s.transcriptLocked(event.ItemID, "assistant", event.Delta, false)
	case "response.output_audio_transcript.done", "response.audio_transcript.done":
		return s.transcriptLocked(event.ItemID, "assistant", event.Transcript, true)
	case "response.output_text.done":
		return s.transcriptLocked(event.ItemID, "assistant", event.Text, true)
	case "response.function_call_arguments.done":
		return s.functionLocked(event.Name, event.CallID, event.Arguments)
	case "response.output_item.done":
		if event.Item.Type == "function_call" {
			return s.functionLocked(event.Item.Name, event.Item.CallID, event.Item.Arguments)
		}
	case "input_audio_buffer.speech_started":
		s.userSpeaking = true
		return []llmtypes.RealtimeEvent{{Kind: "speech_started", ItemID: event.ItemID}}, nil
	case "input_audio_buffer.speech_stopped":
		s.userSpeaking = false
		// VAD creates the next response; do not race it with response.create.
	case "response.created":
		s.responseActive = true
		// Only acknowledged progress/results are known to be in the new response's
		// context. Output sent while response.created was in flight still
		// needs a later response; do not silently lose it in that race.
		if s.pendingOutput == "" && s.pendingItem == "" {
			s.responsePending = false
		}
	case "conversation.item.created", "conversation.item.added":
		if event.Item.Type == "function_call_output" && event.Item.CallID == s.pendingOutput {
			s.pendingOutput = ""
		}
		if event.Item.ID == s.pendingItem {
			s.pendingItem = ""
		}
	case "response.done":
		s.responseActive = false
		return nil, s.respondLocked(s.ctx)
	case "output_audio_buffer.started":
		s.audioPlaying = true
	case "output_audio_buffer.stopped", "output_audio_buffer.cleared":
		s.audioPlaying = false
		return nil, s.respondLocked(s.ctx)
	}
	return nil, nil
}

func (s *realtimeSession) handleNativeLocked(event realtimeWireEvent) ([]llmtypes.RealtimeEvent, error) {
	if s.nativePending != "" && time.Since(s.nativePendingAt) > 30*time.Second {
		if err := s.refuseNativePendingLocked(); err != nil {
			return nil, err
		}
	}
	switch event.Type {
	case "input_transcript.added", "output_transcript.added":
		role := "assistant"
		if event.Type == "input_transcript.added" {
			role = "user"
		}
		id := s.nativeTurns[role]
		if id == "" {
			id = event.Item.ID
			if id == "" {
				id = "native-" + strconv.Itoa(len(s.transcripts))
			}
			s.nativeTurns[role] = id
		}
		if role == "user" {
			s.nativeUser = id
			if s.nativePending != "" && s.nativePendingItem == "" {
				s.nativePendingItem = id
			}
		}
		return s.transcriptLocked(id, role, event.Item.Text, false)
	case "turn.done":
		if event.Turn.Role != "user" && event.Turn.Role != "assistant" {
			return nil, nil
		}
		if event.Turn.ID != "" {
			if s.nativeCompleted[event.Turn.ID] {
				return nil, nil
			}
			if len(s.nativeCompleted) >= realtimeMaxItems {
				return nil, errors.New("realtime session reached its turn limit")
			}
			s.nativeCompleted[event.Turn.ID] = true
		}
		id := s.nativeTurns[event.Turn.Role]
		delete(s.nativeTurns, event.Turn.Role)
		if id == "" {
			id = event.Turn.ID
			if id == "" {
				id = "native-" + strconv.Itoa(len(s.transcripts))
			}
		}
		// Native transcript-item IDs and turn IDs differ. Keep the first item's
		// ID for the entire utterance so a final replaces, rather than duplicates.
		text := event.Turn.Transcript
		if strings.TrimSpace(text) == "" {
			// term-llm's frameless controller seals the collected partial here;
			// a missing/empty final is not an instruction to erase the utterance.
			text = s.transcripts[id].text
		}
		events, err := s.transcriptLocked(id, event.Turn.Role, text, true)
		if err != nil {
			return nil, err
		}
		if event.Turn.Role == "user" {
			s.nativeUser = id
			if s.nativePending != "" && s.nativePendingItem == "" {
				s.nativePendingItem = id
			}
			delegation, err := s.resolveNativePendingLocked()
			return append(events, delegation...), err
		}
		// Do not reuse an earlier user's chat after the assistant finished
		// answering it, or attach an unbound handoff to some future utterance.
		if s.nativeTurns["user"] == "" {
			s.nativeUser = ""
			if s.nativePending != "" && s.nativePendingItem == "" {
				return events, s.refuseNativePendingLocked()
			}
		}
		return events, nil
	case "delegation.created":
		if event.Item.Type != "delegation" || event.Item.Target != "client" {
			return nil, nil
		}
		var text strings.Builder
		for _, part := range event.Item.Content {
			if part.Type == "input_text" {
				text.WriteString(part.Text)
			}
		}
		if strings.TrimSpace(text.String()) != "" {
			events, err := s.delegateLocked(event.Item.ID, text.String())
			if len(events) > 0 {
				s.nativeUsed = s.nativeUser
			}
			return events, err
		}
		id := event.Item.ID
		if id == "" || len(id) > 256 || len(s.handoffs) >= realtimeMaxItems {
			return nil, errors.New("realtime provider returned an invalid handoff")
		}
		if _, exists := s.handoffs[id]; exists {
			return nil, nil
		}
		if s.nativePending != "" {
			// Two textless delegations cannot safely claim the same speech.
			if err := s.refuseNativePendingLocked(); err != nil {
				return nil, err
			}
			s.nativePending = id
			return nil, s.refuseNativePendingLocked()
		}
		s.handoffs[id] = false
		s.nativePending, s.nativePendingItem = id, s.nativeUser
		s.nativePendingAt = time.Now()
		return s.resolveNativePendingLocked()
	}
	return nil, nil
}

// A native metadata-only delegation is explicit authorization to hand off, but
// not a task. Associate it with one adjacent user turn and wait for final text.
// This mirrors term-llm's transcript fallback without executing partial/stale
// speech, reusing a turn, or inferring a delegation from words alone.
func (s *realtimeSession) resolveNativePendingLocked() ([]llmtypes.RealtimeEvent, error) {
	if s.nativePending == "" || s.nativePendingItem == "" {
		return nil, nil
	}
	if s.nativePendingItem == s.nativeUsed {
		return nil, s.refuseNativePendingLocked()
	}
	transcript := s.transcripts[s.nativePendingItem]
	if !transcript.final {
		return nil, nil
	}
	if strings.TrimSpace(transcript.text) == "" {
		return nil, s.refuseNativePendingLocked()
	}
	event := llmtypes.RealtimeEvent{
		Kind: "delegate", HandoffID: s.nativePending,
		ItemID: s.nativePendingItem, Text: transcript.text,
	}
	s.nativeUsed = s.nativePendingItem
	s.nativePending, s.nativePendingItem = "", ""
	return []llmtypes.RealtimeEvent{event}, nil
}

func (s *realtimeSession) refuseNativePendingLocked() error {
	id := s.nativePending
	if s.nativePendingItem != "" {
		s.nativeUsed = s.nativePendingItem
	}
	s.nativePending, s.nativePendingItem = "", ""
	s.handoffs[id] = true
	return s.appendNativeLocked(s.ctx, id, "speakable", "No coding task was started: I could not safely identify the requested task. Please ask the user to repeat the request.")
}

func (s *realtimeSession) transcriptLocked(id, role, text string, final bool) ([]llmtypes.RealtimeEvent, error) {
	if id == "" || len(id) > 256 {
		return nil, errors.New("realtime provider returned an invalid transcript ID")
	}
	previous, exists := s.transcripts[id]
	if previous.final {
		return nil, nil
	}
	if !exists && len(s.transcripts) >= realtimeMaxItems {
		return nil, errors.New("realtime session reached its turn limit")
	}
	if !final {
		text = previous.text + text
	}
	if len(text) > realtimeMaxText {
		return nil, errors.New("realtime transcript exceeded its size limit")
	}
	s.transcripts[id] = realtimeTranscript{text: text, final: final}
	return []llmtypes.RealtimeEvent{{Kind: "transcript", ID: id, Role: role, Text: text, Final: final}}, nil
}

func (s *realtimeSession) functionLocked(name, callID, arguments string) ([]llmtypes.RealtimeEvent, error) {
	if name != realtimeDelegateTool {
		return nil, nil
	}
	var args struct{ Request string }
	if len(arguments) > realtimeMaxText || json.Unmarshal([]byte(arguments), &args) != nil {
		return nil, errors.New("realtime provider returned an invalid handoff")
	}
	return s.delegateLocked(callID, args.Request)
}

func (s *realtimeSession) delegateLocked(id, text string) ([]llmtypes.RealtimeEvent, error) {
	if id == "" || len(id) > 256 || strings.TrimSpace(text) == "" || len(text) > realtimeMaxText {
		return nil, errors.New("realtime provider returned an invalid handoff")
	}
	if _, exists := s.handoffs[id]; exists {
		return nil, nil
	}
	if len(s.handoffs) >= realtimeMaxItems {
		return nil, errors.New("realtime session reached its handoff limit")
	}
	s.handoffs[id] = false
	// Neither protocol supplies a reliable input-transcript link on this event.
	// In particular, GA item_id identifies the function call, not the user's item.
	return []llmtypes.RealtimeEvent{{Kind: "delegate", HandoffID: id, Text: text}}, nil
}
