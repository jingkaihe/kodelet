package base

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateShortSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		called := false
		summary := GenerateShortSummary(
			ctx,
			"summary prompt",
			func(_ context.Context, prompt string, useWeakModel bool) (string, error) {
				assert.Equal(t, "summary prompt", prompt)
				assert.True(t, useWeakModel)
				return "generated summary", nil
			},
			func(error) {
				called = true
			},
		)

		assert.Equal(t, "generated summary", summary)
		assert.False(t, called)
	})

	t.Run("error with callback", func(t *testing.T) {
		var gotErr error
		summary := GenerateShortSummary(
			ctx,
			"summary prompt",
			func(context.Context, string, bool) (string, error) {
				return "", errors.New("generation failed")
			},
			func(err error) {
				gotErr = err
			},
		)

		assert.Equal(t, "Could not generate summary.", summary)
		require.Error(t, gotErr)
		assert.Contains(t, gotErr.Error(), "generation failed")
	})

	t.Run("error without callback", func(t *testing.T) {
		summary := GenerateShortSummary(
			ctx,
			"summary prompt",
			func(context.Context, string, bool) (string, error) {
				return "", errors.New("generation failed")
			},
			nil,
		)

		assert.Equal(t, "Could not generate summary.", summary)
	})
}
