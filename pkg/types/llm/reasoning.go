package llm

import (
	"slices"
	"strings"

	"github.com/pkg/errors"
)

const DefaultReasoningEffort = "medium"

var validReasoningEfforts = []string{
	"none",
	"minimal",
	"low",
	"medium",
	"high",
	"xhigh",
	"max",
}

// NormalizeReasoningEffort normalizes and validates a reasoning effort value.
func NormalizeReasoningEffort(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "", nil
	}
	if !slices.Contains(validReasoningEfforts, normalized) {
		return "", errors.Errorf("invalid reasoning_effort %q; valid values are: %s", raw, strings.Join(validReasoningEfforts, ", "))
	}
	return normalized, nil
}

// NormalizeReasoningConfig normalizes the configured effort and validates its
// optional selection policy. The policy applies when creating conversations;
// persisted conversation snapshots are allowed to outlive later policy changes.
func NormalizeReasoningConfig(config *Config) error {
	if config == nil {
		return nil
	}

	effort, err := NormalizeReasoningEffort(config.ReasoningEffort)
	if err != nil {
		return err
	}
	if effort == "" {
		effort = DefaultReasoningEffort
	}
	config.ReasoningEffort = effort

	seen := make(map[string]struct{}, len(config.AllowedReasoningEfforts))
	allowed := make([]string, 0, len(config.AllowedReasoningEfforts))
	for _, raw := range config.AllowedReasoningEfforts {
		normalized, err := NormalizeReasoningEffort(raw)
		if err != nil {
			return errors.Wrap(err, "invalid allowed_reasoning_efforts")
		}
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		allowed = append(allowed, normalized)
	}
	config.AllowedReasoningEfforts = allowed

	if len(allowed) > 0 && !slices.Contains(allowed, effort) {
		return errors.Errorf("reasoning_effort %q is not included in allowed_reasoning_efforts", effort)
	}
	return nil
}

// ReasoningEffortOptions returns the selectable efforts for a new conversation.
// Without an explicit policy, only the configured value is exposed to UIs while
// direct CLI/API overrides retain their historical unrestricted behavior.
func ReasoningEffortOptions(config Config) []string {
	if len(config.AllowedReasoningEfforts) > 0 {
		return append([]string(nil), config.AllowedReasoningEfforts...)
	}
	effort := strings.TrimSpace(config.ReasoningEffort)
	if effort == "" {
		effort = DefaultReasoningEffort
	}
	return []string{effort}
}
