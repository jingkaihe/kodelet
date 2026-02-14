package base

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactContextWithSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("load prompt failure", func(t *testing.T) {
		err := CompactContextWithSummary(
			ctx,
			func(context.Context) (string, error) {
				return "", errors.New("load error")
			},
			func(context.Context, string, bool) (string, error) {
				t.Fatal("runUtilityPrompt should not be called")
				return "", nil
			},
			func(context.Context, string) error {
				t.Fatal("swapContext should not be called")
				return nil
			},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load compact prompt")
	})

	t.Run("summary generation failure", func(t *testing.T) {
		err := CompactContextWithSummary(
			ctx,
			func(context.Context) (string, error) {
				return "compact prompt", nil
			},
			func(context.Context, string, bool) (string, error) {
				return "", errors.New("run error")
			},
			func(context.Context, string) error {
				t.Fatal("swapContext should not be called")
				return nil
			},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to generate compact summary")
	})

	t.Run("swap failure is returned", func(t *testing.T) {
		swapErr := errors.New("swap error")
		err := CompactContextWithSummary(
			ctx,
			func(context.Context) (string, error) {
				return "compact prompt", nil
			},
			func(context.Context, string, bool) (string, error) {
				return "summary text", nil
			},
			func(context.Context, string) error {
				return swapErr
			},
		)

		require.Error(t, err)
		assert.Equal(t, swapErr, err)
	})

	t.Run("success", func(t *testing.T) {
		var gotPrompt string
		var gotUseWeak bool
		var gotSummary string

		err := CompactContextWithSummary(
			ctx,
			func(context.Context) (string, error) {
				return "compact prompt", nil
			},
			func(_ context.Context, prompt string, useWeak bool) (string, error) {
				gotPrompt = prompt
				gotUseWeak = useWeak
				return "summary text", nil
			},
			func(_ context.Context, summary string) error {
				gotSummary = summary
				return nil
			},
		)

		require.NoError(t, err)
		assert.Equal(t, "compact prompt", gotPrompt)
		assert.False(t, gotUseWeak)
		assert.Equal(t, "summary text", gotSummary)
	})
}
