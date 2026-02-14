package base

import (
	"context"

	"github.com/pkg/errors"
)

// CompactContextWithSummary loads a prompt, runs a utility prompt to produce a summary,
// and swaps context to that summary.
func CompactContextWithSummary(
	ctx context.Context,
	loadPrompt func(ctx context.Context) (string, error),
	runUtilityPrompt func(ctx context.Context, prompt string, useWeakModel bool) (string, error),
	swapContext func(ctx context.Context, summary string) error,
) error {
	compactPrompt, err := loadPrompt(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to load compact prompt")
	}

	summary, err := runUtilityPrompt(ctx, compactPrompt, false)
	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	return swapContext(ctx, summary)
}
