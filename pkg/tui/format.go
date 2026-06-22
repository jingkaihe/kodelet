package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func displayProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || strings.EqualFold(profile, "default") {
		return "default"
	}
	return profile
}

func profileForRequest(profile string) string {
	if strings.EqualFold(strings.TrimSpace(profile), "default") {
		return ""
	}
	return strings.TrimSpace(profile)
}

func normalizeProfileOptions(options []string, selected string) []string {
	selected = displayProfile(selected)
	seen := map[string]bool{}
	normalized := make([]string, 0, len(options)+1)

	appendOption := func(profile string) {
		profile = displayProfile(profile)
		key := strings.ToLower(profile)
		if seen[key] {
			return
		}
		seen[key] = true
		normalized = append(normalized, profile)
	}

	appendOption("default")
	for _, option := range options {
		appendOption(option)
	}
	appendOption(selected)
	return normalized
}

func profileOptionIndex(options []string, profile string) int {
	profile = displayProfile(profile)
	for i, option := range options {
		if strings.EqualFold(displayProfile(option), profile) {
			return i
		}
	}
	return -1
}

func profileFromMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	rawProfile, ok := metadata["profile"].(string)
	if !ok {
		return ""
	}
	return displayProfile(rawProfile)
}

func formatUsage(usage llmtypes.Usage) string {
	cost := usage.TotalCost()
	if usage.MaxContextWindow <= 0 {
		return fmt.Sprintf("$%.2f", cost)
	}
	pct := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
	return fmt.Sprintf("%s/%s (%.0f%%) · $%.2f", formatTokenCount(usage.CurrentContextWindow), formatTokenCount(usage.MaxContextWindow), pct, cost)
}

func renderExitSummary(conversationID string, usage llmtypes.Usage) string {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}

	lines := []string{
		fmt.Sprintf("Conversation ID: %s", conversationID),
		fmt.Sprintf("Token usage: %s input · %s output · %s cache write · %s cache read · %s total", formatTokenCount(usage.InputTokens), formatTokenCount(usage.OutputTokens), formatTokenCount(usage.CacheCreationInputTokens), formatTokenCount(usage.CacheReadInputTokens), formatTokenCount(usage.TotalTokens())),
	}
	if usage.MaxContextWindow > 0 {
		pct := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
		lines = append(lines, fmt.Sprintf("Context window: %s/%s (%.0f%%)", formatTokenCount(usage.CurrentContextWindow), formatTokenCount(usage.MaxContextWindow), pct))
	}
	lines = append(lines,
		fmt.Sprintf("Cost: $%.4f", usage.TotalCost()),
		fmt.Sprintf("Resume: kodelet chat -r %s", conversationID),
	)
	return strings.Join(lines, "\n")
}

func formatTokenCount(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fK", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

func displayCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "~"
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return cwd
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if utf8.RuneCountInString(id) <= 8 {
		return id
	}
	runes := []rune(id)
	return string(runes[:8])
}
