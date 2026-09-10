package artifacts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())
	store, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store, path
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	require.NoError(t, png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 3, 2))))
	return output.Bytes()
}

func TestPutGetAndScope(t *testing.T) {
	store, path := testStore(t)
	data := testPNG(t)
	input := tooltypes.ToolAttachment{
		Type: "image", Path: "/runner/cache/picture.png", MimeType: "text/html", Alt: "Generated picture",
		ArtifactID: "untrusted", ShortCode: "untrusted", ViewURL: "https://old.example/image", Error: "old error",
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
	assert.Equal(t, filepath.Join(filepath.Dir(path), "artifacts", attachment.ArtifactID), localPath)
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
	require.NoError(t, store.Close())
	reopened, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer reopened.Close()
	_, _, err = reopened.Get(t.Context(), "conversation", attachment.ArtifactID)
	require.NoError(t, err)
	_, err = reopened.db.Exec(`DELETE FROM conversation_artifacts WHERE artifact_id = ?`, attachment.ArtifactID)
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
	require.NoError(t, gif.EncodeAll(&animation, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{0, 1}}))
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"png", testPNG(t)}, {"jpeg", jpg.Bytes()}, {"gif", animation.Bytes()}, {"webp", webp},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attachment, err := store.Put(t.Context(), "conversation", "call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(tt.data))
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
	require.NoError(t, store.db.Get(&count, `SELECT COUNT(*) FROM image_artifacts`))
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
	_, err := store.Put(t.Context(), "conversation", "call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(encoded.Bytes()))
	require.ErrorContains(t, err, "256-frame limit")

	encoded.Reset()
	animation.Image, animation.Delay = animation.Image[:2], animation.Delay[:2]
	require.NoError(t, gif.EncodeAll(&encoded, animation))
	truncated := encoded.Bytes()[:encoded.Len()-4]
	_, err = store.Put(t.Context(), "conversation", "call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(truncated))
	require.Error(t, err)
}

func TestStartupSweep(t *testing.T) {
	store, path := testStore(t)
	old := time.Now().UTC().Add(-48 * time.Hour)
	data := testPNG(t)
	put := func(conversationID string, age bool) tooltypes.ToolAttachment {
		attachment, err := store.Put(t.Context(), conversationID, "call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(data))
		require.NoError(t, err)
		if age {
			_, err = store.db.Exec(`UPDATE image_artifacts SET created_at = ? WHERE id = ?`, old, attachment.ArtifactID)
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
	results, err := json.Marshal(map[string]tooltypes.StructuredToolResult{"call": {ToolName: "image", Success: true, Attachments: []tooltypes.ToolAttachment{checkpointed}}})
	require.NoError(t, err)
	_, err = store.db.Exec(`INSERT INTO conversations (id, raw_messages, provider, usage, tool_results, created_at, updated_at) VALUES (?, '[]', 'openai', '{}', ?, ?, ?)`, "checkpointed", string(results), old, old)
	require.NoError(t, err)
	_, err = store.db.Exec(`INSERT INTO runner_registrations (id, owner_id, host_instance_id, workspace_path, workspace_name, status, created_at, updated_at)
		VALUES ('runner', 'local', 'host', '/work', 'work', 'online', ?, ?)`, old, old)
	require.NoError(t, err)
	_, err = store.db.Exec(`INSERT INTO runner_runs (id, conversation_id, runner_id, status, created_at, updated_at)
		VALUES ('run', 'active', 'runner', 'running', ?, ?)`, old, old)
	require.NoError(t, err)
	for _, name := range []string{".upload-orphan", "art_orphan", ".upload-fresh"} {
		file := filepath.Join(store.dir, name)
		require.NoError(t, os.WriteFile(file, data, 0o600))
		if name != ".upload-fresh" {
			require.NoError(t, os.Chtimes(file, old, old))
		}
	}
	require.NoError(t, store.Close())
	reopened, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer reopened.Close()
	_, _, err = reopened.Get(t.Context(), "abandoned", abandoned.ArtifactID)
	assert.ErrorIs(t, err, ErrNotFound)
	for _, name := range []string{abandoned.ArtifactID, ".upload-orphan", "art_orphan"} {
		assert.NoFileExists(t, filepath.Join(store.dir, name))
	}
	for conversationID, attachment := range map[string]tooltypes.ToolAttachment{"fresh": fresh, "checkpointed": checkpointed, "active": active} {
		_, file, err := reopened.Get(t.Context(), conversationID, attachment.ArtifactID)
		require.NoError(t, err)
		assert.FileExists(t, file)
	}
	assert.FileExists(t, filepath.Join(store.dir, ".upload-fresh"))
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
