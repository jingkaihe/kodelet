package controlplane

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const (
	realtimeProgressInterval = 200 * time.Millisecond
	realtimeProgressMaxText  = 16 << 10
	realtimeProgressMaxQueue = 64
	realtimeProgressMaxBytes = 256 << 10
	realtimeProgressOmitted  = "\n[Part of this progress update was omitted.]"
	realtimeDeliveryTimeout  = 15 * time.Second
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
		c.progress = &realtimeProgress{
			call:  c,
			turns: make(map[string]*realtimeProgressTurn),
			wake:  make(chan struct{}, 1),
		}
		go c.progress.run()
	}
	return c.progress
}

// electHandoff makes one of the turn's remaining handoffs receive its progress.
func (t *realtimeProgressTurn) electHandoff() {
	t.handoffID = ""
	for id := range t.handoffs {
		t.handoffID = id
		return
	}
}

func (p *realtimeProgress) bind(turnID, handoffID string, joining bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	turn := p.turns[turnID]
	if turn == nil {
		turn = &realtimeProgressTurn{
			turnID:    turnID,
			handoffID: handoffID,
			handoffs:  make(map[string]struct{}),
			skipBlock: joining,
		}
		p.turns[turnID] = turn
	}
	turn.handoffs[handoffID] = struct{}{}
	if turn.handoffID == "" && !turn.completed {
		// Every earlier handoff left this turn. Progress resumes for the new
		// one, which may have missed the start of the block in flight.
		turn.handoffID = handoffID
		turn.skipBlock = joining
	}
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
		turn.electHandoff()
	}
	if turn.completed && len(turn.handoffs) == 0 {
		delete(p.turns, turnID)
	}
}

// forgetLocked stops routing progress to a resolved handoff. A completed turn
// left with no handoffs has nothing more to deliver or to answer quietly.
func (p *realtimeProgress) forgetLocked(handoffID string) {
	for turnID, turn := range p.turns {
		if _, bound := turn.handoffs[handoffID]; !bound {
			continue
		}
		delete(turn.handoffs, handoffID)
		if turn.handoffID == handoffID {
			turn.electHandoff()
		}
		if turn.completed && len(turn.handoffs) == 0 {
			delete(p.turns, turnID)
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
	case "text-delta", "content-end":
	case "text":
		// Streaming handlers deliver the agent's complete text only as structured
		// text, such as a cited search answer. Plain text events are host
		// notices, like steering acknowledgements, and must not be spoken as
		// progress or end the agent's block in flight.
		if event.TextData == nil {
			return
		}
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
		if blockEnd {
			p.resumeLocked(turn)
		}
		return
	}
	text := event.Delta
	if event.Kind == "text" {
		text, _ = event.Content.(string)
	}
	tooLong := len(turn.buffer)+len(text) > realtimeProgressMaxText ||
		len(turn.block)+len(text) > realtimeProgressMaxText
	if tooLong {
		turn.omitted = true
		p.flushLocked(turn, true)
		if blockEnd {
			p.resumeLocked(turn)
		}
		return
	}
	turn.buffer += text
	turn.block += text
	if blockEnd {
		p.flushLocked(turn, true)
	}
}

// resumeLocked ends an omission at a block boundary, once the queue has room.
func (p *realtimeProgress) resumeLocked(turn *realtimeProgressTurn) {
	if len(p.queue) < realtimeProgressMaxQueue && p.queuedBytes < realtimeProgressMaxBytes {
		turn.omitted = false
		turn.omissionSent = false
	}
}

func (p *realtimeProgress) enqueueLocked(turn *realtimeProgressTurn, update llmtypes.RealtimeUpdate) {
	p.queue = append(p.queue, realtimeDelivery{
		turnID:    turn.turnID,
		handoffID: turn.handoffID,
		update:    update,
	})
	p.queuedBytes += len(update.Text)
}

func (p *realtimeProgress) flushLocked(turn *realtimeProgressTurn, blockEnd bool) {
	if turn.handoffID == "" || turn.completed || turn.omissionSent {
		return
	}
	hasText := turn.buffer != "" || blockEnd && turn.block != ""
	if !turn.omitted && hasText {
		if len(p.queue) >= realtimeProgressMaxQueue || p.queuedBytes+len(turn.buffer) > realtimeProgressMaxBytes {
			turn.omitted = true
		} else {
			p.enqueueLocked(turn, llmtypes.RealtimeUpdate{Text: turn.buffer, BlockEnd: blockEnd})
		}
	}
	turn.buffer = ""
	if turn.omitted {
		// Reserve one small boundary marker per affected turn, even when the
		// optional queue is full. Never leave a truncated block looking complete.
		p.enqueueLocked(turn, llmtypes.RealtimeUpdate{Text: realtimeProgressOmitted, BlockEnd: true})
		turn.omissionSent = true
		turn.lastBlock = ""
		turn.block = ""
	} else if blockEnd && turn.block != "" {
		turn.lastBlock = turn.block
		turn.block = ""
		turn.lastBlockHandoff = turn.handoffID
	}
	p.signal()
}

// Completion must not sit behind a backlog of now-obsolete progress. Retain
// at most one pending update, or replace the backlog with a block boundary
// marking the omission. In-flight writes and terminal deliveries stay ordered.
func (p *realtimeProgress) compactLocked(turn *realtimeProgressTurn) {
	pending := func(delivery realtimeDelivery) bool {
		return delivery.done == nil &&
			delivery.turnID == turn.turnID &&
			delivery.handoffID == turn.handoffID
	}
	count := 0
	for _, delivery := range p.queue {
		if pending(delivery) {
			count++
		}
	}
	if count <= 1 {
		return
	}
	kept := p.queue[:0]
	for _, delivery := range p.queue {
		if pending(delivery) {
			p.queuedBytes -= len(delivery.update.Text)
		} else {
			kept = append(kept, delivery)
		}
	}
	clear(p.queue[len(kept):])
	p.queue = kept
	turn.buffer = ""
	turn.block = ""
	turn.lastBlock = ""
	turn.omitted = true
	turn.omissionSent = false
}

// sealLocked ends a turn's progress when its first handoff completes, and
// returns what that handoff should be told. Any later handoff of the same turn
// resolves quietly, so the turn's result is never spoken twice.
func (p *realtimeProgress) sealLocked(turn *realtimeProgressTurn, handoffID, result string) string {
	if turn.completed {
		return ""
	}
	p.compactLocked(turn)
	p.flushLocked(turn, true)
	turn.completed = true
	if handoffID != turn.lastBlockHandoff && result == turn.lastBlock {
		return "The delegated turn ended. Its final response was already provided in the preceding agent update; do not repeat it."
	}
	return result
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
		result = p.sealLocked(turn, handoffID, result)
	}
	p.forgetLocked(handoffID)
	done := make(chan struct{})
	p.queue = append(p.queue, realtimeDelivery{
		turnID:    turnID,
		handoffID: handoffID,
		result:    result,
		done:      done,
	})
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
			p.flushBuffered()
		case <-p.wake:
		}
		if !p.drain() {
			return
		}
	}
}

// flushBuffered sends text that has waited a full interval without a block end.
func (p *realtimeProgress) flushBuffered() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, turn := range p.turns {
		if turn.buffer != "" {
			p.flushLocked(turn, false)
		}
	}
}

func (p *realtimeProgress) next() (realtimeDelivery, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return realtimeDelivery{}, false
	}
	delivery := p.queue[0]
	p.queue[0] = realtimeDelivery{}
	p.queue = p.queue[1:]
	p.queuedBytes -= len(delivery.update.Text)
	return delivery, true
}

func (p *realtimeProgress) deliver(delivery realtimeDelivery) error {
	ctx, cancel := context.WithTimeout(p.call.ctx, realtimeDeliveryTimeout)
	defer cancel()
	if delivery.done != nil {
		defer close(delivery.done)
		return p.call.session.Complete(ctx, delivery.handoffID, delivery.result)
	}
	return p.call.session.Update(ctx, delivery.handoffID, delivery.update)
}

// drain delivers queued items in order. It reports false once the call has
// ended because a result could not be delivered.
func (p *realtimeProgress) drain() bool {
	for p.call.ctx.Err() == nil {
		delivery, ok := p.next()
		if !ok {
			return true
		}
		err := p.deliver(delivery)
		switch {
		case err == nil:
		case delivery.done != nil:
			// The provider can no longer be kept consistent without the result.
			p.call.emit(realtimeEvent{
				Kind: "error",
				Text: "The agent's output is in the conversation, but the spoken update could not be delivered.",
			})
			p.call.close()
			return false
		case p.call.ctx.Err() == nil:
			// Progress is optional. A session that cannot continue ends its event
			// stream, which closes the call; anything else only loses this update.
			logger.G(p.call.ctx).WithError(err).Warn("could not deliver live voice progress")
			p.omit(delivery)
		}
	}
	return true
}

// omit closes the block of an undelivered update with the omission marker, so
// the provider's incomplete copy of it never looks complete. The rest of the
// block in flight is dropped too, unless the lost update ended its block and
// nothing of the next one has been taken yet.
func (p *realtimeProgress) omit(delivery realtimeDelivery) {
	p.mu.Lock()
	defer p.mu.Unlock()
	turn := p.turns[delivery.turnID]
	if turn == nil || turn.completed || turn.omitted || turn.handoffID != delivery.handoffID {
		return
	}
	atBoundary := delivery.update.BlockEnd && turn.buffer == "" && turn.block == ""
	turn.omitted = true
	turn.omissionSent = false
	p.flushLocked(turn, true)
	if atBoundary {
		turn.omitted = false
		turn.omissionSent = false
	}
}
