package artifacts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testStore(t testing.TB) (*Store, *db.DB) {
	t.Helper()
	database := dbtest.OpenTemp(t)
	store, err := Open(t.Context(), database)
	require.NoError(t, err)
	return store, database
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	require.NoError(t, png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 3, 2))))
	return output.Bytes()
}

func TestPutGetAndScope(t *testing.T) {
	store, database := testStore(t)
	data := testPNG(t)
	input := tooltypes.ToolAttachment{
		Type:       "image",
		Path:       "/runner/cache/picture.png",
		MimeType:   "text/html",
		Alt:        "Generated picture",
		ArtifactID: "untrusted",
		ShortCode:  "untrusted",
		ViewURL:    "https://old.example/image",
		Error:      "old error",
	}
	attachment, err := store.Put(t.Context(), "conversation", "call", input, bytes.NewReader(data))
	require.NoError(t, err)
	assert.Regexp(t, `^art_[A-Za-z0-9_-]{22}$`, attachment.ArtifactID)
	assert.Regexp(t, `^[A-Za-z0-9_-]{22}$`, attachment.ShortCode)
	assert.NotEqual(t, strings.TrimPrefix(attachment.ArtifactID, "art_"), attachment.ShortCode)
	assert.Empty(t, attachment.Path)
	assert.Empty(t, attachment.ViewURL)
	assert.Empty(t, attachment.Error)
	assert.Equal(t, "picture.png", attachment.Filename)
	assert.Equal(t, "image/png", attachment.MimeType)
	assert.Equal(t, input.Alt, attachment.Alt)
	assert.Equal(t, 3, attachment.Width)
	assert.Equal(t, 2, attachment.Height)
	assert.Equal(t, int64(len(data)), attachment.Size)

	got, localPath, err := store.Get(t.Context(), "conversation", attachment.ArtifactID)
	require.NoError(t, err)
	expected := attachment
	expected.Alt = "" // Captions belong to the tool result, not shared image metadata.
	assert.Equal(t, expected, got)
	assert.Equal(t, filepath.Join(filepath.Dir(database.Path()), "artifacts", attachment.ArtifactID), localPath)
	stored, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, data, stored)
	info, err := os.Stat(localPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	_, _, err = store.Get(t.Context(), "another-conversation", attachment.ArtifactID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, _, err = store.GetByShortCode(t.Context(), attachment.ArtifactID)
	assert.ErrorIs(t, err, ErrNotFound)
	got, resolved, err := store.GetByShortCode(t.Context(), attachment.ShortCode)
	require.NoError(t, err)
	assert.Equal(t, expected, got)
	assert.Equal(t, localPath, resolved)

	// Identical bytes are separate immutable uploads, not replacements.
	second, err := store.Put(t.Context(), "conversation", "call", input, bytes.NewReader(data))
	require.NoError(t, err)
	assert.NotEqual(t, attachment.ArtifactID, second.ArtifactID)
	// Reopening preserves uploaded images.
	reopened, err := Open(t.Context(), database)
	require.NoError(t, err)
	_, _, err = reopened.Get(t.Context(), "conversation", attachment.ArtifactID)
	require.NoError(t, err)
	_, err = reopened.db.Writer.Exec(`DELETE FROM conversation_artifacts WHERE artifact_id = ?`, attachment.ArtifactID)
	require.NoError(t, err)
	_, _, err = reopened.GetByShortCode(t.Context(), attachment.ShortCode)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSupportedImageFormats(t *testing.T) {
	store, _ := testStore(t)
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var jpg, animation bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpg, img, nil))
	palette := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(0, 0, 3, 2), palette)
	require.NoError(t, gif.EncodeAll(&animation, &gif.GIF{
		Image: []*image.Paletted{frame, frame},
		Delay: []int{0, 1},
	}))
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"png", testPNG(t)},
		{"jpeg", jpg.Bytes()},
		{"gif", animation.Bytes()},
		{"webp", webp},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attachment, err := store.Put(t.Context(), "conversation", "call",
				tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(tt.data),
			)
			require.NoError(t, err)
			assert.Equal(t, "image/"+tt.name, attachment.MimeType)
		})
	}
}

func TestRejectInvalidImages(t *testing.T) {
	store, _ := testStore(t)
	data := testPNG(t)
	oversized := bytes.Clone(data)
	binary.BigEndian.PutUint32(oversized[16:20], 100_000)
	binary.BigEndian.PutUint32(oversized[20:24], 100_000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	for _, tt := range []struct {
		name   string
		source io.Reader
		want   string
	}{
		{"empty", strings.NewReader(""), "invalid image header"},
		{"svg", strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "invalid image header"},
		{"truncated", bytes.NewReader(data[:len(data)-12]), "invalid image data"},
		{"pixels", bytes.NewReader(oversized), "decoded pixel limit"},
		{"bytes", io.LimitReader(zeroReader{}, MaxBytes+1), "byte limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Put(t.Context(), "conversation", "call", tooltypes.ToolAttachment{Type: "image"}, tt.source)
			require.ErrorContains(t, err, tt.want)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := store.Put(ctx, "conversation", "call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(data))
	assert.ErrorIs(t, err, context.Canceled)
	_, err = store.Put(t.Context(), "", "call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(data))
	assert.ErrorContains(t, err, "conversation ID")
	_, err = store.Put(t.Context(), "conversation", "call", tooltypes.ToolAttachment{Type: "file"}, bytes.NewReader(data))
	assert.ErrorContains(t, err, "image attachment")
	var count int
	require.NoError(t, store.db.Writer.Get(&count, `SELECT COUNT(*) FROM image_artifacts`))
	assert.Zero(t, count)
	files, err := os.ReadDir(store.dir)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestGIFBudgetAndCorruptLaterFrames(t *testing.T) {
	store, _ := testStore(t)
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	animation := &gif.GIF{}
	for range 257 {
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 0)
	}
	var encoded bytes.Buffer
	require.NoError(t, gif.EncodeAll(&encoded, animation))
	_, err := store.Put(t.Context(), "conversation", "call",
		tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(encoded.Bytes()),
	)
	require.ErrorContains(t, err, "256-frame limit")

	encoded.Reset()
	animation.Image, animation.Delay = animation.Image[:2], animation.Delay[:2]
	require.NoError(t, gif.EncodeAll(&encoded, animation))
	truncated := encoded.Bytes()[:encoded.Len()-4]
	_, err = store.Put(t.Context(), "conversation", "call",
		tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(truncated),
	)
	require.Error(t, err)
}

func TestSweep(t *testing.T) {
	store, database := testStore(t)
	old := time.Now().UTC().Add(-48 * time.Hour)
	data := testPNG(t)
	put := func(conversationID string, age bool) tooltypes.ToolAttachment {
		attachment, err := store.Put(t.Context(), conversationID, "call",
			tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(data),
		)
		require.NoError(t, err)
		if age {
			_, err = store.db.Writer.Exec(`UPDATE image_artifacts SET created_at = ? WHERE id = ?`, old, attachment.ArtifactID)
			require.NoError(t, err)
			file := filepath.Join(store.dir, attachment.ArtifactID)
			require.NoError(t, os.Chtimes(file, old, old))
		}
		return attachment
	}
	abandoned := put("abandoned", true)
	fresh := put("fresh", false)
	checkpointed := put("checkpointed", true)
	active := put("active", true)
	results, err := json.Marshal(map[string]tooltypes.StructuredToolResult{
		"call": {
			ToolName:    "image",
			Success:     true,
			Attachments: []tooltypes.ToolAttachment{checkpointed},
		},
	})
	require.NoError(t, err)
	_, err = store.db.Writer.Exec(
		`INSERT INTO conversations (id, raw_messages, provider, usage, tool_results, created_at, updated_at) VALUES (?, '[]', 'openai', '{}', ?, ?, ?)`,
		"checkpointed", string(results), old, old,
	)
	require.NoError(t, err)
	_, err = store.db.Writer.Exec(`INSERT INTO runner_registrations (id, owner_id, host_instance_id, workspace_path, workspace_name, status, created_at, updated_at)
		VALUES ('runner', 'local', 'host', '/work', 'work', 'online', ?, ?)`, old, old)
	require.NoError(t, err)
	_, err = store.db.Writer.Exec(`INSERT INTO runner_runs (id, conversation_id, runner_id, status, created_at, updated_at)
		VALUES ('run', 'active', 'runner', 'running', ?, ?)`, old, old)
	require.NoError(t, err)
	for _, name := range []string{".upload-orphan", "art_orphan", ".upload-fresh"} {
		file := filepath.Join(store.dir, name)
		require.NoError(t, os.WriteFile(file, data, 0o600))
		if name != ".upload-fresh" {
			require.NoError(t, os.Chtimes(file, old, old))
		}
	}
	// Opening is initialization only; the server runs cleanup separately.
	reopened, err := Open(t.Context(), database)
	require.NoError(t, err)
	_, _, err = reopened.Get(t.Context(), "abandoned", abandoned.ArtifactID)
	require.NoError(t, err)
	require.NoError(t, reopened.Sweep(t.Context()))
	_, _, err = reopened.Get(t.Context(), "abandoned", abandoned.ArtifactID)
	assert.ErrorIs(t, err, ErrNotFound)
	for _, name := range []string{abandoned.ArtifactID, ".upload-orphan", "art_orphan"} {
		assert.NoFileExists(t, filepath.Join(store.dir, name))
	}
	for conversationID, attachment := range map[string]tooltypes.ToolAttachment{
		"fresh":        fresh,
		"checkpointed": checkpointed,
		"active":       active,
	} {
		_, file, err := reopened.Get(t.Context(), conversationID, attachment.ArtifactID)
		require.NoError(t, err)
		assert.FileExists(t, file)
	}
	assert.FileExists(t, filepath.Join(store.dir, ".upload-fresh"))
}

// Seed metadata without image encoding so cleanup tests can exercise multiple
// pages and benchmarks can vary the number of references independently of JSON.
func seedCleanupReferences(t testing.TB, database *db.DB, conversationID string, count int) []artifactReference {
	t.Helper()
	old := time.Now().UTC().Add(-48 * time.Hour)
	tx, err := database.Writer.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	refs := make([]artifactReference, count)
	for i := range count {
		refs[i] = artifactReference{ToolCallID: fmt.Sprintf("call-%03d", i), ArtifactID: fmt.Sprintf("art_%s_%03d", conversationID, i)}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO image_artifacts
			(id, short_code, filename, mime_type, width, height, size, created_at)
			VALUES (?, ?, 'image.png', 'image/png', 1, 1, 1, ?)`, refs[i].ArtifactID, refs[i].ArtifactID, old)
		require.NoError(t, err)
		_, err = tx.ExecContext(t.Context(), `INSERT INTO conversation_artifacts
			(conversation_id, tool_call_id, artifact_id) VALUES (?, ?, ?)`, conversationID, refs[i].ToolCallID, refs[i].ArtifactID)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	return refs
}

func saveCleanupConversation(t testing.TB, database *db.DB, id, results string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := database.Writer.ExecContext(t.Context(), `INSERT INTO conversations
		(id, raw_messages, provider, usage, tool_results, created_at, updated_at)
		VALUES (?, '[]', 'openai', '{}', ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET tool_results = excluded.tool_results, updated_at = excluded.updated_at`,
		id, results, now, now)
	require.NoError(t, err)
}

func TestSweepCheckpointedReferencesDoNotAcquireWriter(t *testing.T) {
	store, database := testStore(t)
	refs := seedCleanupReferences(t, database, "saved", 2)
	saveCleanupConversation(t, database, "saved", fmt.Sprintf(`{
		"call-000": {"attachments": [{"artifactId": %q}], "data": %q, "metadataType": "unknown"},
		"call-001": {"attachments": [{"artifactId": %q}]}
	}`, refs[0].ArtifactID, strings.Repeat("large tool output", 4096), refs[1].ArtifactID))
	writer, err := database.Writer.Connx(t.Context())
	require.NoError(t, err)
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// Holding the only writer makes any attempted acquisition time out.
	require.NoError(t, store.Sweep(ctx))
	var count int
	require.NoError(t, database.Reader.Get(&count, `SELECT COUNT(*) FROM conversation_artifacts`))
	assert.Equal(t, 2, count)
}

func TestSweepRechecksSnapshotBeforeDeleting(t *testing.T) {
	for _, test := range []struct {
		name     string
		exists   bool
		mutation string
	}{
		{name: "concurrent checkpoint", exists: true, mutation: "save"},
		{name: "first checkpoint", mutation: "save"},
		{name: "conversation removed", exists: true, mutation: "delete"},
		{name: "run started", mutation: "run"},
		{name: "age changed", mutation: "age"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, database := testStore(t)
			refs := seedCleanupReferences(t, database, "conversation", 1)
			if test.exists {
				saveCleanupConversation(t, database, "conversation", `{}`)
			}
			cutoff := time.Now().UTC().Add(-cleanupGrace)
			snapshot, err := store.abandonedReferences(t.Context(), "conversation", cutoff)
			require.NoError(t, err)
			require.Equal(t, refs, snapshot.abandoned)
			switch test.mutation {
			case "save":
				saveCleanupConversation(t, database, "conversation", fmt.Sprintf(
					`{"call-000": {"attachments": [{"artifactId": %q}]}}`, refs[0].ArtifactID))
			case "delete":
				_, err = database.Writer.Exec(`DELETE FROM conversations WHERE id = 'conversation'`)
				require.NoError(t, err)
			case "run":
				now := time.Now().UTC()
				_, err = database.Writer.Exec(`INSERT INTO runner_registrations
					(id, owner_id, host_instance_id, workspace_path, workspace_name, status, created_at, updated_at)
					VALUES ('runner', 'local', 'host', '/work', 'work', 'online', ?, ?)`, now, now)
				require.NoError(t, err)
				_, err = database.Writer.Exec(`INSERT INTO runner_runs
					(id, conversation_id, runner_id, status, created_at, updated_at)
					VALUES ('run', 'conversation', 'runner', 'opening', ?, ?)`, now, now)
				require.NoError(t, err)
			case "age":
				_, err = database.Writer.Exec(`UPDATE image_artifacts SET created_at = ?`, time.Now().UTC())
				require.NoError(t, err)
			}
			_, err = store.pruneReferences(t.Context(), "conversation", snapshot, snapshot.abandoned, cutoff)
			require.NoError(t, err)
			var count int
			require.NoError(t, database.Reader.Get(&count, `SELECT COUNT(*) FROM conversation_artifacts`))
			assert.Equal(t, 1, count, "references changed after selection must survive")
		})
	}
}

func TestSweepUnreadableResultsAreRetained(t *testing.T) {
	store, database := testStore(t)
	for i, results := range []string{`{broken`, ``, `{"call-000": {"attachments": "broken"}}`} {
		id := fmt.Sprintf("unreadable-%d", i)
		seedCleanupReferences(t, database, id, 1)
		saveCleanupConversation(t, database, id, results)
	}
	seedCleanupReferences(t, database, "abandoned", 1)
	require.NoError(t, store.Sweep(t.Context()))
	var count int
	require.NoError(t, database.Reader.Get(&count, `SELECT COUNT(*) FROM conversation_artifacts`))
	assert.Equal(t, 3, count, "unreadable history must not cause deletions or stop cleanup of other conversations")
}

func TestSweepBatchesAndSharedImages(t *testing.T) {
	store, database := testStore(t)
	// More than one page of conversations, references, and orphan metadata.
	for i := range cleanupBatchSize + 1 {
		seedCleanupReferences(t, database, fmt.Sprintf("conversation-%03d", i), 1)
	}
	seedCleanupReferences(t, database, "many-references", cleanupBatchSize+1)
	refs := seedCleanupReferences(t, database, "saved", 1)
	saveCleanupConversation(t, database, "saved", fmt.Sprintf(
		`{"call-000": {"attachments": [{"artifactId": %q}]}}`, refs[0].ArtifactID))
	for _, id := range []string{"saved", "abandoned-sharing-image"} {
		_, err := database.Writer.Exec(`INSERT INTO conversation_artifacts VALUES (?, 'wrong-call', ?)`, id, refs[0].ArtifactID)
		require.NoError(t, err)
	}
	require.NoError(t, store.Sweep(t.Context()))
	var remaining []artifactReference
	require.NoError(t, database.Reader.Select(&remaining, `SELECT tool_call_id, artifact_id FROM conversation_artifacts`))
	assert.Equal(t, refs, remaining, "retain only the exact checkpointed (tool call, artifact) pair")
	var ids []string
	require.NoError(t, database.Reader.Select(&ids, `SELECT id FROM image_artifacts`))
	assert.Equal(t, []string{refs[0].ArtifactID}, ids, "shared image survives abandoned-reference cleanup")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, store.Sweep(ctx), context.Canceled)
}

func TestSweepRetainsMetadataReferencedAfterSelection(t *testing.T) {
	store, database := testStore(t)
	refs := seedCleanupReferences(t, database, "orphan", 1)
	_, err := database.Writer.Exec(`DELETE FROM conversation_artifacts`)
	require.NoError(t, err)
	writer, err := database.Writer.Connx(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan error, 1)
	defer func() {
		cancel()
		writer.Close()
		<-done
	}()
	waits := database.Writer.Stats().WaitCount
	go func() {
		defer close(done)
		done <- store.Sweep(ctx)
	}()
	require.Eventually(t, func() bool {
		return database.Writer.Stats().WaitCount > waits
	}, time.Second, time.Millisecond, "cleanup selected the orphan and is waiting to delete it")
	_, err = writer.ExecContext(t.Context(), `INSERT INTO conversation_artifacts
		(conversation_id, tool_call_id, artifact_id) VALUES ('new-reference', 'call', ?)`, refs[0].ArtifactID)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, <-done)
	var count int
	require.NoError(t, database.Reader.Get(&count, `SELECT COUNT(*) FROM image_artifacts`))
	assert.Equal(t, 1, count, "the delete must recheck references, not trust the earlier orphan selection")
}

func BenchmarkSweepCheckpointedReferences(b *testing.B) {
	store, database := testStore(b)
	// Eight 1 MiB snapshots with 32 images each model the repeated-parsing
	// bottleneck, without copying any real conversation data.
	for i := range 8 {
		id := fmt.Sprintf("conversation-%d", i)
		refs := seedCleanupReferences(b, database, id, 32)
		results := make(map[string]any, len(refs)+1)
		results["large-output"] = map[string]any{"data": strings.Repeat("x", 1<<20)}
		for _, ref := range refs {
			results[ref.ToolCallID] = map[string]any{"attachments": []map[string]string{{"artifactId": ref.ArtifactID}}}
		}
		raw, err := json.Marshal(results)
		require.NoError(b, err)
		saveCleanupConversation(b, database, id, string(raw))
	}
	for b.Loop() {
		require.NoError(b, store.Sweep(b.Context()))
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
