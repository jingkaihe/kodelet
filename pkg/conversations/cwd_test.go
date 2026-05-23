package conversations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCWD(t *testing.T) {
	t.Run("blank", func(t *testing.T) {
		cwd, err := NormalizeCWD(" \t\n ")
		require.NoError(t, err)
		assert.Empty(t, cwd)
	})

	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		cwd, err := NormalizeCWD(dir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(dir), cwd)
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		linkPath := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(dir, linkPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		cwd, err := NormalizeCWD(linkPath)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(dir), cwd)
	})

	t.Run("missing path", func(t *testing.T) {
		cwd, err := NormalizeCWD(filepath.Join(t.TempDir(), "missing"))
		assert.Empty(t, cwd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cwd directory does not exist")
	})

	t.Run("file path", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("data"), 0o644))

		cwd, err := NormalizeCWD(filePath)
		assert.Empty(t, cwd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cwd is not a directory")
	})
}

func TestCurrentWorkingDirectory(t *testing.T) {
	originalCWD, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalCWD))
	})

	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	cwd, err := CurrentWorkingDirectory()
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(dir), cwd)
}

func TestResolveCWD(t *testing.T) {
	ctx := context.Background()
	requested := t.TempDir()
	defaultDir := t.TempDir()
	stored := t.TempDir()

	t.Run("new conversation uses requested cwd", func(t *testing.T) {
		store := newCWDTestStore()

		resolution, err := ResolveCWD(ctx, store, "", requested, defaultDir, false)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(requested), resolution.CWD)
		assert.False(t, resolution.Locked)
		assert.Empty(t, resolution.ConversationID)
	})

	t.Run("new conversation falls back to default cwd", func(t *testing.T) {
		store := newCWDTestStore()

		resolution, err := ResolveCWD(ctx, store, "", "", defaultDir, false)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(defaultDir), resolution.CWD)
	})

	t.Run("missing conversation may be new when not required", func(t *testing.T) {
		store := newCWDTestStore()

		resolution, err := ResolveCWD(ctx, store, "conv-1", requested, defaultDir, false)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(requested), resolution.CWD)
		assert.Equal(t, "conv-1", resolution.ConversationID)
		assert.False(t, resolution.Locked)
	})

	t.Run("missing required conversation returns load error", func(t *testing.T) {
		store := newCWDTestStore()

		resolution, err := ResolveCWD(ctx, store, "conv-1", requested, defaultDir, true)
		assert.Nil(t, resolution)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conversation not found")
	})

	t.Run("stored cwd locks conversation", func(t *testing.T) {
		store := newCWDTestStore()
		require.NoError(t, store.Save(ctx, convtypes.ConversationRecord{ID: "conv-1", CWD: stored}))

		resolution, err := ResolveCWD(ctx, store, "conv-1", "", defaultDir, true)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(stored), resolution.CWD)
		assert.True(t, resolution.Locked)
		assert.False(t, resolution.LegacyRecord)
		assert.Equal(t, "conv-1", resolution.ConversationID)
		require.NotNil(t, resolution.Record)
		assert.Equal(t, stored, resolution.Record.CWD)
	})

	t.Run("matching requested cwd is accepted for stored cwd", func(t *testing.T) {
		store := newCWDTestStore()
		require.NoError(t, store.Save(ctx, convtypes.ConversationRecord{ID: "conv-1", CWD: stored}))

		resolution, err := ResolveCWD(ctx, store, "conv-1", stored, defaultDir, true)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(stored), resolution.CWD)
		assert.True(t, resolution.Locked)
	})

	t.Run("conflicting requested cwd is rejected", func(t *testing.T) {
		store := newCWDTestStore()
		require.NoError(t, store.Save(ctx, convtypes.ConversationRecord{ID: "conv-1", CWD: stored}))

		resolution, err := ResolveCWD(ctx, store, "conv-1", requested, defaultDir, true)
		assert.Nil(t, resolution)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCWDConflict)
	})

	t.Run("legacy record uses requested cwd and marks locked", func(t *testing.T) {
		store := newCWDTestStore()
		require.NoError(t, store.Save(ctx, convtypes.ConversationRecord{ID: "conv-1"}))

		resolution, err := ResolveCWD(ctx, store, "conv-1", requested, defaultDir, true)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(requested), resolution.CWD)
		assert.True(t, resolution.Locked)
		assert.True(t, resolution.LegacyRecord)
	})

	t.Run("errors when no cwd can be resolved", func(t *testing.T) {
		store := newCWDTestStore()

		resolution, err := ResolveCWD(ctx, store, "", "", "", false)
		assert.Nil(t, resolution)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "working directory could not be resolved")
	})
}

type cwdTestStore struct {
	records map[string]convtypes.ConversationRecord
}

func newCWDTestStore() *cwdTestStore {
	return &cwdTestStore{records: make(map[string]convtypes.ConversationRecord)}
}

func (s *cwdTestStore) Save(_ context.Context, record convtypes.ConversationRecord) error {
	s.records[record.ID] = record
	return nil
}

func (s *cwdTestStore) Load(_ context.Context, id string) (convtypes.ConversationRecord, error) {
	record, ok := s.records[id]
	if !ok {
		return convtypes.ConversationRecord{}, errors.New("conversation not found")
	}
	return record, nil
}

func (s *cwdTestStore) Delete(_ context.Context, id string) error {
	delete(s.records, id)
	return nil
}

func (s *cwdTestStore) Query(_ context.Context, options convtypes.QueryOptions) (convtypes.QueryResult, error) {
	return convtypes.QueryResult{QueryOptions: options}, nil
}

func (s *cwdTestStore) Close() error { return nil }
