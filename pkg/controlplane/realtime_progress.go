package controlplane

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/chat"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const (
	realtimeProgressInterval = 200 * time.Millisecond
	realtimeProgressMaxText  = 16 << 10
	realtimeProgressMaxQueue = 64
	realtimeProgressMaxBytes = 256 << 10
	realtimeProgressOmitted  = "\n[Part of this progress update was omitted.]"
)

// Progress is an optional observer of execution. One writer per call orders
// updates before results without letting provider latency block a coding turn.
type realtimeProgress struct {
	call        *realtimeCall
	mu          sync.Mutex
	turns       map[string]*realtimeProgressTurn
	queue       []realtimeDelivery
	queuedBytes int
	wake        chan struct{}
}

type realtimeProgressTurn struct {
	turnID           string
	handoffID        string
	handoffs         map[string]struct{}
	buffer           string
	block            string
	lastBlock        string
	lastBlockHandoff string
	omitted          bool
	omissionSent     bool
	completed        bool
	skipBlock        bool
}

type realtimeDelivery struct {
	turnID    string
	handoffID string
	update    llmtypes.RealtimeUpdate
	result    string
	done      chan struct{} // Non-nil only for terminal deliveries; these are never dropped.
}

func (c *realtimeCall) progressBridge() *realtimeProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.progress == nil {
		c.progress = &realtimeProgress{call: c, turns: make(map[string]*realtimeProgressTurn), wake: make(chan struct{}, 1)}
		go c.progress.run()
	}
	return c.progress
}

func (p *realtimeProgress) bind(turnID, handoffID string, joining bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.turns[turnID] == nil {
		p.turns[turnID] = &realtimeProgressTurn{
			turnID: turnID, handoffID: handoffID, handoffs: make(map[string]struct{}), skipBlock: joining,
		}
	}
	p.turns[turnID].handoffs[handoffID] = struct{}{}
}

// A steer that missed the old turn becomes a new turn. Finish its old context
// before rebinding, but do not resolve the handoff until its own work finishes.
func (p *realtimeProgress) unbind(turnID, handoffID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	turn := p.turns[turnID]
	if turn == nil {
		return
	}
	delete(turn.handoffs, handoffID)
	if turn.handoffID == handoffID {
		// Completion has sealed the queue, including any pending block end.
		// Rebinding must not remove text that its acknowledgement refers to.
		if !turn.completed {
			p.compactLocked(turn)
			p.flushLocked(turn, true)
		}
		turn.handoffID = ""
		for id := range turn.handoffs {
			turn.handoffID = id
			break
		}
	}
}

// The turn ID is captured by runAdmittedChat, not looked up at delivery time.
// This also observes typed turns subsequently steered by voice, without making
// the observer an approval owner or forwarding the same text per follow-up.
func (s *Server) broadcastRealtimeProgress(conversationID, turnID string, event chat.ChatEvent) {
	if event.Role != "assistant" || turnID == "" {
		return
	}
	switch event.Kind {
	case "text-delta", "text", "content-end":
	default:
		return
	}
	s.realtimeMu.Lock()
	var call *realtimeCall
	for _, candidate := range s.realtimeCalls {
		if candidate.chat.ConversationID == conversationID {
			call = candidate
			break
		}
	}
	s.realtimeMu.Unlock()
	if call == nil {
		return
	}
	call.mu.Lock()
	progress := call.progress
	call.mu.Unlock()
	if progress != nil {
		progress.observe(turnID, event)
	}
}

func (p *realtimeProgress) observe(turnID string, event chat.ChatEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	turn := p.turns[turnID]
	if turn == nil || turn.handoffID == "" || turn.completed || p.call.ctx.Err() != nil {
		return
	}
	blockEnd := event.Kind == "content-end" || event.Kind == "text"
	// Joining an existing turn may miss a negation or other essential prefix.
	// Ignore that in-flight block; a complete text event is safe by itself.
	if turn.skipBlock {
		turn.skipBlock = !blockEnd
		if event.Kind != "text" {
			return
		}
	}
	if turn.omitted {
		if blockEnd && len(p.queue) < realtimeProgressMaxQueue && p.queuedBytes < realtimeProgressMaxBytes {
			turn.omitted, turn.omissionSent = false, false
		}
		return
	}
	text := event.Delta
	if event.Kind == "text" {
		text, _ = event.Content.(string)
	}
	if len(turn.buffer)+len(text) > realtimeProgressMaxText || len(turn.block)+len(text) > realtimeProgressMaxText {
		turn.omitted = true
		p.flushLocked(turn, true)
		if blockEnd && len(p.queue) < realtimeProgressMaxQueue && p.queuedBytes < realtimeProgressMaxBytes {
			turn.omitted, turn.omissionSent = false, false
		}
		return
	}
	turn.buffer += text
	turn.block += text
	if blockEnd {
		p.flushLocked(turn, true)
	}
}

func (p *realtimeProgress) flushLocked(turn *realtimeProgressTurn, blockEnd bool) {
	if turn.handoffID == "" || turn.completed || turn.omissionSent {
		return
	}
	if !turn.omitted && (turn.buffer != "" || blockEnd && turn.block != "") {
		if len(p.queue) >= realtimeProgressMaxQueue || p.queuedBytes+len(turn.buffer) > realtimeProgressMaxBytes {
			turn.omitted = true
		} else {
			p.queue = append(p.queue, realtimeDelivery{turnID: turn.turnID, handoffID: turn.handoffID, update: llmtypes.RealtimeUpdate{Text: turn.buffer, BlockEnd: blockEnd}})
			p.queuedBytes += len(turn.buffer)
		}
	}
	turn.buffer = ""
	if turn.omitted {
		// Reserve one small boundary marker per affected turn, even when the
		// optional queue is full. Never leave a truncated block looking complete.
		p.queue = append(p.queue, realtimeDelivery{turnID: turn.turnID, handoffID: turn.handoffID, update: llmtypes.RealtimeUpdate{Text: realtimeProgressOmitted, BlockEnd: true}})
		p.queuedBytes += len(realtimeProgressOmitted)
		turn.omissionSent = true
		turn.lastBlock, turn.block = "", ""
	} else if blockEnd && turn.block != "" {
		turn.lastBlock, turn.block = turn.block, ""
		turn.lastBlockHandoff = turn.handoffID
	}
	p.signal()
}

// Completion must not sit behind a backlog of now-obsolete progress. Retain
// at most one pending update, or replace the backlog with a block boundary
// marking the omission. In-flight writes and terminal deliveries stay ordered.
func (p *realtimeProgress) compactLocked(turn *realtimeProgressTurn) {
	count := 0
	for _, delivery := range p.queue {
		if delivery.done == nil && delivery.turnID == turn.turnID && delivery.handoffID == turn.handoffID {
			count++
		}
	}
	if count <= 1 {
		return
	}
	kept := p.queue[:0]
	for _, delivery := range p.queue {
		if delivery.done == nil && delivery.turnID == turn.turnID && delivery.handoffID == turn.handoffID {
			p.queuedBytes -= len(delivery.update.Text)
		} else {
			kept = append(kept, delivery)
		}
	}
	clear(p.queue[len(kept):])
	p.queue = kept
	turn.buffer, turn.block, turn.lastBlock = "", "", ""
	turn.omitted, turn.omissionSent = true, false
}

func (p *realtimeProgress) complete(handoffID, result, turnID string) {
	if p.call.ctx.Err() != nil {
		return
	}
	if strings.TrimSpace(result) == "" {
		result = "The delegated turn ended without a text result."
	}
	result = boundedRealtimeResult(result)
	p.mu.Lock()
	if turn := p.turns[turnID]; turn != nil {
		if turn.completed {
			result = "" // Resolve another tool call for this turn without repeating speech.
		} else {
			p.compactLocked(turn)
			p.flushLocked(turn, true)
			turn.completed = true
			if handoffID != turn.lastBlockHandoff && result == turn.lastBlock {
				result = "The delegated turn ended. Its final response was already provided in the preceding agent update; do not repeat it."
			}
		}
	}
	done := make(chan struct{})
	p.queue = append(p.queue, realtimeDelivery{turnID: turnID, handoffID: handoffID, result: result, done: done})
	p.mu.Unlock()
	p.signal()
	select {
	case <-done:
	case <-p.call.ctx.Done():
	}
}

func boundedRealtimeResult(result string) string {
	const limit = 16000
	if len(result) <= limit {
		return result
	}
	n := limit
	for !utf8.RuneStart(result[n]) {
		n--
	}
	return result[:n] + "\n[See the conversation for the full result.]"
}

func (p *realtimeProgress) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *realtimeProgress) run() {
	ticker := time.NewTicker(realtimeProgressInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.call.ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			for _, turn := range p.turns {
				if turn.buffer != "" {
					p.flushLocked(turn, false)
				}
			}
			p.mu.Unlock()
		case <-p.wake:
		}
		for p.call.ctx.Err() == nil {
			p.mu.Lock()
			if len(p.queue) == 0 {
				p.mu.Unlock()
				break
			}
			delivery := p.queue[0]
			p.queue[0] = realtimeDelivery{}
			p.queue = p.queue[1:]
			p.queuedBytes -= len(delivery.update.Text)
			p.mu.Unlock()
			ctx, cancel := context.WithTimeout(p.call.ctx, 15*time.Second)
			var err error
			if delivery.done != nil {
				err = p.call.session.Complete(ctx, delivery.handoffID, delivery.result)
				close(delivery.done)
			} else {
				err = p.call.session.Update(ctx, delivery.handoffID, delivery.update)
			}
			cancel()
			if err != nil {
				p.call.emit(realtimeEvent{Kind: "error", Text: "The agent's output is in the conversation, but the spoken update could not be delivered."})
				p.call.close()
				return
			}
		}
	}
}
