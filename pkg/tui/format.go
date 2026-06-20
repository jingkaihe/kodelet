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
	if profile == "" {
		return "default"
	}
	return profile
}

func profileForRequest(profile string) string {
	if strings.TrimSpace(profile) == "default" {
		return ""
	}
	return strings.TrimSpace(profile)
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
