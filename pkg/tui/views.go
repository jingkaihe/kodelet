package tui

import (
	"fmt"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// FormatUsageStats formats usage statistics for display
func FormatUsageStats(usage llmtypes.Usage) (usageText string, costText string) {
	if usage.TotalTokens() == 0 {
		return "", ""
	}

	// Build context window display with percentage if available
	var ctxDisplay string
	if usage.MaxContextWindow > 0 {
		percentage := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
		ctxDisplay = fmt.Sprintf("Ctx: %d / %d (%.1f%%)", usage.CurrentContextWindow, usage.MaxContextWindow, percentage)
	} else {
		ctxDisplay = fmt.Sprintf("Ctx: %d / %d", usage.CurrentContextWindow, usage.MaxContextWindow)
	}

	usageText = fmt.Sprintf("Tokens: %d in / %d out / %d cw / %d cr / %d total | %s",
		usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens(),
		ctxDisplay)

	// Add cost information if available
	if usage.TotalCost() > 0 {
		costText = fmt.Sprintf(" | Cost: $%.4f", usage.TotalCost())
	}

	return usageText, costText
}

// GetSpinnerChar returns the spinner character for the given index
func GetSpinnerChar(index int) string {
	spinChars := []string{".", "∘", "○", "◌", "◍", "◉", "◎", "●"}
	return spinChars[index%len(spinChars)]
}

// ShouldShowCommandDropdown determines if the command dropdown should be shown
func ShouldShowCommandDropdown(input string, commands []string, isProcessing bool) bool {
	if isProcessing {
		return false
	}

	if !strings.HasPrefix(input, "/") {
		return false
	}

	if IsCommandComplete(input, commands) {
		return false
	}

	return true
}

// FormatModelInfo formats the provider and model name for display
func FormatModelInfo(provider, model string) string {
	return fmt.Sprintf("%s/%s", provider, model)
}
