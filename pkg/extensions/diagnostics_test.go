package extensions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticSinkContext(t *testing.T) {
	sink := newRecordingDiagnosticSink()
	ctx := ContextWithDiagnosticSink(context.Background(), sink)

	got, ok := DiagnosticSinkFromContext(ctx)
	require.True(t, ok)
	assert.Same(t, sink, got)

	ctx = ContextWithDiagnosticSink(context.Background(), nil)
	got, ok = DiagnosticSinkFromContext(ctx)
	assert.False(t, ok)
	assert.Nil(t, got)
}
