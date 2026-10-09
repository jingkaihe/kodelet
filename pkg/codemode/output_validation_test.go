package codemode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOutputDataDiagnostics(t *testing.T) {
	t.Run("mismatches report locations without values", func(t *testing.T) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"id"},
			"properties": map[string]any{
				"items": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			},
		}
		err := ValidateOutputData(schema, json.RawMessage(`{"items":[1,"private-output"]}`))
		var validationErr *OutputValidationError
		require.True(t, errors.As(err, &validationErr))
		assert.Equal(t, "child reply data does not match its declared outputSchema; the tool will not be retried", err.Error())
		assert.ElementsMatch(t, []string{"/: required id", "/items/1: type"}, validationErr.Details)
		assert.NotContains(t, strings.Join(validationErr.Details, "\n"), "private-output")
	})

	t.Run("schema errors keep the compiler diagnosis", func(t *testing.T) {
		err := ValidateOutputData(map[string]any{"type": "not-a-type"}, json.RawMessage(`{}`))
		var validationErr *OutputValidationError
		require.True(t, errors.As(err, &validationErr))
		assert.Contains(t, err.Error(), "outputSchema is invalid")
		assert.NotContains(t, err.Error(), "not-a-type", "the script-facing message stays sanitized")
		require.Len(t, validationErr.Details, 1)
		assert.Contains(t, validationErr.Details[0], "metaschema")
	})

	t.Run("details are bounded", func(t *testing.T) {
		schema := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
		err := ValidateOutputData(schema, json.RawMessage(`["a","b","c","d","e","f","g","h","i","j"]`))
		var validationErr *OutputValidationError
		require.True(t, errors.As(err, &validationErr))
		assert.Len(t, validationErr.Details, maxValidationDetails)
	})
}
