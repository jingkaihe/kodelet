package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

type voiceSteerOutcome struct {
	done    chan struct{}
	pending bool
	err     error
}

// Called while the old run is still registered, so a new run cannot consume a
// late steer between checking the queue and deciding whether it needs a turn.
func (s *Server) finishVoiceSteersLocked(conversationID string, run *activeChatRun) {
	if len(run.voiceSteers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for sourceID, outcome := range run.voiceSteers {
		result, err := s.turns.db.ExecContext(ctx,
			"DELETE FROM steering_messages WHERE conversation_id=? AND source_id=?", conversationID, sourceID)
		if err == nil {
			var rows int64
			rows, err = result.RowsAffected()
			outcome.pending = rows == 1
		}
		outcome.err = err
		close(outcome.done)
	}
	run.voiceSteers = nil
}

func (c *realtimeCall) delegate(event llmtypes.RealtimeEvent) {
	if event.HandoffID == "" || len(event.HandoffID) > 256 || strings.TrimSpace(event.Text) == "" || len(event.Text) > 10000 {
		c.emit(realtimeEvent{Kind: "error", Text: "Live voice returned an invalid coding request."})
		return
	}
	c.mu.Lock()
	_, duplicate := c.handoffs[event.HandoffID]
	if duplicate || c.closed {
		c.mu.Unlock()
		return
	}
	if len(c.handoffs) >= 256 {
		c.mu.Unlock()
		c.emit(realtimeEvent{Kind: "error", Text: "Live voice reached its task limit. Start a new call."})
		return
	}
	c.handoffs[event.HandoffID] = struct{}{}
	c.mu.Unlock()
	// The provider session never owns accepted coding work. Even a queued steer
	// that misses the finishing turn must be resolved after the caller hangs up.
	c.work.Add(1)
	go func() {
		defer c.work.Done()
		ctx := c.server.chatExecutionContext(c.ctx)
		receipt, err := c.dispatch(ctx, event)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("live voice handoff failed")
			c.complete(event.HandoffID, "The coding request could not be confirmed. Do not retry it automatically; inspect the conversation before submitting it again.", "")
			c.emit(realtimeEvent{Kind: "error", Text: "Could not confirm the coding request. Check the conversation before trying again."})
			return
		}
		result := "The coding turn ended with status: " + receipt.Status
		if receipt.Result != nil {
			result = *receipt.Result
		}
		c.complete(event.HandoffID, result, receipt.TurnID)
	}()
}

func (c *realtimeCall) dispatch(ctx context.Context, event llmtypes.RealtimeEvent) (chat.TurnReceipt, error) {
	s := c.server
	req := c.chat
	req.Message = event.Text
	sourceID := c.id + ":" + event.HandoffID
	req.UserMessageSource = sourceID
	digest := sha256.Sum256([]byte(sourceID))
	req.TurnID = "voice-" + hex.EncodeToString(digest[:])
	admissionDeadline := time.Now().Add(5 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return chat.TurnReceipt{}, err
		}
		// Coordinate queue insertion with unregisterActiveChat. Checking isActive
		// then enqueuing without this lock can strand a task after the final poll.
		s.activeChatsMu.Lock()
		run := s.activeChats[req.ConversationID]
		if s.stopping || run != nil && run.stopRequested {
			s.activeChatsMu.Unlock()
			return chat.TurnReceipt{}, errors.New("conversation is stopping")
		}
		if run != nil {
			if run.turnID == "" {
				s.activeChatsMu.Unlock()
				return chat.TurnReceipt{}, errors.New("conversation is busy with an interactive operation")
			}
			outcome := &voiceSteerOutcome{done: make(chan struct{})}
			_, err := s.turns.db.ExecContext(ctx, `INSERT INTO steering_messages
				(conversation_id,content,images_json,created_at,source_id) VALUES (?,?,'[]',?,?)`,
				req.ConversationID, req.Message, time.Now().UTC(), sourceID)
			if err == nil {
				if run.voiceSteers == nil {
					run.voiceSteers = make(map[string]*voiceSteerOutcome)
				}
				run.voiceSteers[sourceID] = outcome
			}
			s.activeChatsMu.Unlock()
			if err != nil {
				return chat.TurnReceipt{}, errors.Wrap(err, "failed to queue voice steering")
			}
			c.emit(realtimeEvent{Kind: "agent-started", TurnID: run.turnID})
			select {
			case <-outcome.done:
			case <-ctx.Done():
				return chat.TurnReceipt{}, ctx.Err()
			}
			if outcome.err != nil {
				return chat.TurnReceipt{}, errors.Wrap(outcome.err, "could not reconcile voice steering")
			}
			if !outcome.pending {
				return s.turns.get(ctx, req.ConversationID, run.turnID)
			}
			// It was definitely never consumed. Re-enter admission; another
			// typed/voice turn may have won while the previous turn was finishing.
			continue
		}
		s.activeChatsMu.Unlock()
		receipt, admitted, err := s.turns.admit(ctx, req)
		if errors.Is(err, errConversationBusy) {
			if time.Now().After(admissionDeadline) {
				return chat.TurnReceipt{}, err
			}
			// Admission precedes active-run registration. Wait for that small
			// gap rather than enqueueing against a nonexistent run or replaying.
			select {
			case <-time.After(10 * time.Millisecond):
				continue
			case <-ctx.Done():
				return chat.TurnReceipt{}, ctx.Err()
			}
		}
		if err != nil {
			return chat.TurnReceipt{}, err
		}
		if !admitted {
			return receipt, nil
		}
		c.emit(realtimeEvent{Kind: "agent-started", TurnID: req.TurnID})
		owner, err := c.waitForOwnerWatch()
		if err != nil {
			_ = s.turns.finish(ctx, req.ConversationID, req.TurnID, "failed", nil, err)
			return chat.TurnReceipt{}, err
		}
		if req.ClientCapabilities != nil {
			caps := *req.ClientCapabilities
			caps.InteractiveUI = caps.InteractiveUI && owner.capabilities.InteractiveUI
			caps.PersistentWidgets = caps.PersistentWidgets && owner.capabilities.PersistentWidgets
			caps.PersistentSurfaces = caps.PersistentSurfaces && owner.capabilities.PersistentSurfaces
			req.ClientCapabilities = &caps
		}
		// Approval ownership uses the real browser lifetime/capabilities; coding
		// execution is detached inside runAdmittedChat, just like normal chat.
		_, err = s.runAdmittedChat(owner.ctx, req, realtimeChatSink{owner: owner}, c.clientID, nil, receipt)
		if err != nil {
			return chat.TurnReceipt{}, err
		}
		return s.turns.get(ctx, req.ConversationID, req.TurnID)
	}
}

// This sink is used only by the approval owner. Ordinary coding events already
// fan out through broadcastingEventSink, so Send must not broadcast them twice.
type realtimeChatSink struct {
	owner *subscriberEventSink
}

func (sink realtimeChatSink) Send(event chat.ChatEvent) error {
	if strings.HasPrefix(event.Kind, "ui-") {
		return sink.owner.Send(event)
	}
	return nil
}

func (c *realtimeCall) waitForOwnerWatch() (*subscriberEventSink, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	for {
		c.server.chatSubscribersMu.Lock()
		for watcher := range c.server.chatSubscribers[c.chat.ConversationID] {
			if watcher.ownerID == c.ownerID && watcher.clientID == c.clientID && watcher.ctx != nil && watcher.ctx.Err() == nil {
				c.server.chatSubscribersMu.Unlock()
				return watcher, nil
			}
		}
		c.server.chatSubscribersMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, errors.New("the owning browser must watch the conversation before coding can start")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *realtimeCall) complete(handoffID, result, turnID string) {
	c.emit(realtimeEvent{Kind: "agent-completed", TurnID: turnID})
	if c.ctx.Err() != nil {
		return
	}
	// Keep the sideband payload bounded, without affecting the durable coding
	// result or its ordinary transcript rendering.
	runes := []rune(result)
	if len(runes) > 16000 {
		result = string(runes[:16000]) + "\n[See the conversation for the full result.]"
	}
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	if err := c.session.Complete(ctx, handoffID, result); err != nil {
		c.emit(realtimeEvent{Kind: "error", Text: "The coding result is in the conversation, but the spoken reply could not be delivered."})
		c.close()
	}
}
