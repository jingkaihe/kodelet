package registry

import (
	"context"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type affinityRecord struct {
	ConversationAffinity
	persisted bool
}

// affinityIndex owns conversation-to-runner selection and active-conversation
// leases. Registry.mu protects it so reservation remains atomic with run
// capacity changes.
type affinityIndex struct {
	records map[string]affinityRecord
	active  map[string]string
}

func newAffinityIndex() *affinityIndex {
	return &affinityIndex{
		records: make(map[string]affinityRecord),
		active:  make(map[string]string),
	}
}

func (a *affinityIndex) get(conversationID string) (affinityRecord, bool) {
	if a == nil {
		return affinityRecord{}, false
	}
	record, ok := a.records[strings.TrimSpace(conversationID)]
	return record, ok
}

func (a *affinityIndex) put(conversationID string, affinity ConversationAffinity, persisted bool) {
	if a == nil {
		return
	}
	affinity.EnvironmentProfile = normalizeEnvironmentProfile(affinity.EnvironmentProfile)
	a.records[strings.TrimSpace(conversationID)] = affinityRecord{ConversationAffinity: affinity, persisted: persisted}
}

func (a *affinityIndex) forget(conversationID string) {
	if a == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	delete(a.records, conversationID)
	delete(a.active, conversationID)
}

func (a *affinityIndex) releasePending(conversationID string) bool {
	if a == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	record, ok := a.records[conversationID]
	if !ok || record.persisted || a.active[conversationID] != "" {
		return false
	}
	delete(a.records, conversationID)
	return true
}

func (a *affinityIndex) activeRun(conversationID string) string {
	if a == nil {
		return ""
	}
	return a.active[strings.TrimSpace(conversationID)]
}

func (a *affinityIndex) activate(conversationID, runID string) {
	if a != nil {
		a.active[strings.TrimSpace(conversationID)] = strings.TrimSpace(runID)
	}
}

func (a *affinityIndex) deactivate(conversationID string) {
	if a != nil {
		delete(a.active, strings.TrimSpace(conversationID))
	}
}

func (a *affinityIndex) removeRunner(runnerID string) int {
	if a == nil {
		return 0
	}
	runnerID = strings.TrimSpace(runnerID)
	removed := 0
	for conversationID, record := range a.records {
		if record.RunnerID != runnerID {
			continue
		}
		delete(a.records, conversationID)
		delete(a.active, conversationID)
		removed++
	}
	return removed
}

// BindConversation establishes authoritative affinity between a conversation and runner.
func (r *Registry) BindConversation(ctx context.Context, conversationID, runnerID string) error {
	return r.BindConversationWithEnvironmentProfile(ctx, conversationID, runnerID, "")
}

// BindConversationWithEnvironmentProfile establishes and immediately persists authoritative affinity.
func (r *Registry) BindConversationWithEnvironmentProfile(ctx context.Context, conversationID, runnerID, environmentProfile string) error {
	conversationID = strings.TrimSpace(conversationID)
	runnerID = strings.TrimSpace(runnerID)
	environmentProfile = normalizeEnvironmentProfile(environmentProfile)
	if conversationID == "" {
		return errors.New("conversation id is required")
	}
	if runnerID == "" {
		return errors.New("runner id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runners[runnerID] == nil {
		return errors.New("runner not found")
	}
	return r.bindConversationLockedContext(ctx, conversationID, runnerID, environmentProfile, r.now().UTC(), true)
}

// CommitConversationAffinity persists a pending first-turn reservation after
// the conversation record itself has been saved.
func (r *Registry) CommitConversationAffinity(ctx context.Context, conversationID string) error {
	if r == nil {
		return errors.New("runner registry is required")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.affinities.get(conversationID)
	if !ok {
		return errors.New("conversation runner affinity is not reserved")
	}
	if record.persisted || r.persistence == nil {
		return nil
	}
	if err := r.persistence.BindConversation(ctx, conversationID, record.RunnerID, record.EnvironmentProfile, r.now().UTC()); err != nil {
		return err
	}
	r.affinities.put(conversationID, record.ConversationAffinity, true)
	return nil
}

// ReleasePendingConversationAffinity forgets an unpublished first-run reservation.
// Durable bindings and active run leases are never removed by this method.
func (r *Registry) ReleasePendingConversationAffinity(conversationID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	released := r.affinities.releasePending(conversationID)
	r.mu.Unlock()
	return released
}

// RunnerForConversation returns the current durable or pending runner affinity.
func (r *Registry) RunnerForConversation(conversationID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.affinities.get(conversationID)
	return record.RunnerID, ok
}

// ResolveConversationAffinity refreshes one durable affinity while retaining a
// pending first-turn reservation that has not created a conversation record yet.
func (r *Registry) ResolveConversationAffinity(ctx context.Context, conversationID string) (ConversationAffinity, bool, error) {
	if r == nil {
		return ConversationAffinity{}, false, errors.New("runner registry is required")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationAffinity{}, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	current, currentExists := r.affinities.get(conversationID)
	if r.persistence == nil {
		return current.ConversationAffinity, currentExists, nil
	}
	affinity, found, err := r.persistence.ConversationAffinity(ctx, conversationID)
	if err != nil {
		return ConversationAffinity{}, false, err
	}
	if !found {
		if currentExists && !current.persisted {
			return current.ConversationAffinity, true, nil
		}
		if currentExists && r.affinities.activeRun(conversationID) != "" {
			if err := r.persistence.BindConversation(ctx, conversationID, current.RunnerID, current.EnvironmentProfile, r.now().UTC()); err != nil {
				return ConversationAffinity{}, false, err
			}
			return current.ConversationAffinity, true, nil
		}
		r.affinities.forget(conversationID)
		return ConversationAffinity{}, false, nil
	}
	if r.runners[affinity.RunnerID] == nil {
		return ConversationAffinity{}, false, errors.New("persisted conversation runner affinity is invalid")
	}
	affinity.EnvironmentProfile = normalizeEnvironmentProfile(affinity.EnvironmentProfile)
	r.affinities.put(conversationID, affinity, true)
	return affinity, true, nil
}

// ForgetConversation removes affinity after central conversation deletion commits.
func (r *Registry) ForgetConversation(conversationID string) {
	if r == nil || strings.TrimSpace(conversationID) == "" {
		return
	}
	r.mu.Lock()
	r.affinities.forget(conversationID)
	r.mu.Unlock()
}

func (r *Registry) reserveConversationAffinityLocked(conversationID, runnerID, environmentProfile string) error {
	return r.bindConversationLockedContext(r.ctx, conversationID, runnerID, environmentProfile, r.now().UTC(), false)
}

func (r *Registry) bindConversationLockedContext(ctx context.Context, conversationID, runnerID, environmentProfile string, now time.Time, persist bool) error {
	if current, exists := r.affinities.get(conversationID); exists {
		if current.RunnerID != runnerID {
			return errors.Errorf("conversation is bound to runner %s", current.RunnerID)
		}
		if current.EnvironmentProfile != environmentProfile {
			return environmentProfileLockedError(current.EnvironmentProfile, environmentProfile)
		}
		if persist && !current.persisted && r.persistence != nil {
			if err := r.persistence.BindConversation(ctx, conversationID, runnerID, environmentProfile, now); err != nil {
				return err
			}
			r.affinities.put(conversationID, current.ConversationAffinity, true)
		}
		return nil
	}
	if persist && r.persistence != nil {
		if err := r.persistence.BindConversation(ctx, conversationID, runnerID, environmentProfile, now); err != nil {
			return err
		}
	}
	r.affinities.put(conversationID, ConversationAffinity{RunnerID: runnerID, EnvironmentProfile: environmentProfile}, persist && r.persistence != nil)
	return nil
}

func normalizeEnvironmentProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if strings.EqualFold(profile, "default") {
		return ""
	}
	return profile
}

func environmentProfileLockedError(locked, requested string) error {
	if locked == "" {
		locked = "default"
	}
	if requested == "" {
		requested = "default"
	}
	return errors.Errorf("conversation environment profile is locked to %q; cannot resume with %q", locked, requested)
}
