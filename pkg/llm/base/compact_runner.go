package base

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/pkg/errors"
)

// CompactContextWithSummary runs the built-in compact prompt to produce a summary,
// then swaps context to that summary.
func CompactContextWithSummary(
	ctx context.Context,
	runUtilityPrompt func(ctx context.Context, prompt string, useWeakModel bool) (string, error),
	swapContext func(ctx context.Context, summary string) error,
) error {
	summary, err := runUtilityPrompt(ctx, prompts.CompactPrompt, false)
	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}
	if strings.TrimSpace(summary) == "" {
		return errors.New("compact summary is empty")
	}

	return swapContext(ctx, summary)
}
