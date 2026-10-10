package agentenv_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/artifacts"
	"github.com/jingkaihe/kodelet/pkg/codemode"
	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeExecutionArtifactSelectionSurvivesHooksSaveAndRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	images, err := artifacts.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, images.Close()) })
	store, err := sqlite.NewStore(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	var content bytes.Buffer
	require.NoError(t, png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	foreign, err := images.Put(t.Context(), "other-conversation", "other-call", tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(content.Bytes()))
	require.NoError(t, err)

	// A real final-result hook removes one selected output but leaves its
	// attachment descriptor behind. It also tries to insert a foreign descriptor.
	local, hook := net.Pipe()
	t.Cleanup(func() { _ = hook.Close() })
	require.NoError(t, hook.SetDeadline(time.Now().Add(15*time.Second)))
	hookDone := make(chan error, 1)
	go func() {
		hookDone <- func() error {
			reader := bufio.NewReader(hook)
			for index := range 2 {
				var length int
				if _, err := fmt.Fscanf(reader, "Content-Length: %d\r\n\r\n", &length); err != nil {
					return err
				}
				body := make([]byte, length)
				if _, err := io.ReadFull(reader, body); err != nil {
					return err
				}
				var request struct {
					ID     int64  `json:"id"`
					Method string `json:"method"`
					Params struct {
						Payload struct {
							Tool struct {
								Output tooltypes.StructuredToolResult `json:"output"`
							} `json:"tool"`
						} `json:"payload"`
					} `json:"params"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					return err
				}
				var result any = extensions.InitializeResult{
					Name: "prune", Subscriptions: []extensions.Subscription{{Event: extensions.EventToolResult}},
				}
				if index == 1 {
					assert.Equal(t, "extension.event.handle", request.Method)
					output := request.Params.Payload.Tool.Output
					metadata := output.Metadata.(tooltypes.CodeExecutionMetadata)
					assert.Len(t, output.Attachments, 3)
					metadata.Items = append(metadata.Items[:1], metadata.Items[2:]...)
					metadata.Items = append(metadata.Items,
						tooltypes.CodeExecutionOutput{Type: "image", ArtifactID: metadata.Items[0].ArtifactID}, // Cannot upgrade retention.
						tooltypes.CodeExecutionOutput{Type: "image", ArtifactID: foreign.ArtifactID},
						metadata.Items[1], // Cannot multiply image emissions.
						tooltypes.CodeExecutionOutput{Type: "image", ArtifactID: metadata.Items[1].ArtifactID, Detail: "original"},
					)
					output.Metadata = metadata
					output.Attachments = append(output.Attachments, foreign)
					encoded, err := json.Marshal(output)
					if err != nil {
						return err
					}
					result = extensions.EventResult{Output: encoded}
				}
				encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(hook, "Content-Length: %d\r\n\r\n%s", len(encoded), encoded); err != nil {
					return err
				}
			}
			return nil
		}()
	}()
	workspace := t.TempDir()
	manager := extensions.NewRuntimeManager()
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	runtime, release, err := manager.RuntimeWithAttachmentsForIsolatedLease(
		t.Context(), t.Context(), workspace, extensions.DefaultConfig(), extensions.ExtensionCallContext{},
		[]extensions.Attachment{{ID: "prune", Transport: local}},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, release()) })
	environment := agentenv.NewLocalEnvironment(workspace, runtime)
	_, err = environment.Open(t.Context(), agentenv.RunSpec{ConversationID: "conversation", Config: llmtypes.Config{CodeMode: "on"}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Close(t.Context())) })
	ctx := tools.ContextWithCodeExecution(t.Context(), tools.CodeExecutionContext{
		Definitions:   []codemode.Definition{{Name: "images"}, {Name: "view_image"}},
		ValidateImage: func(string) error { return nil },
		Call: func(ctx context.Context, _, _, callID string, _ func(tools.CodeToolReply)) (tools.CodeToolReply, error) {
			var reply tools.CodeToolReply
			for range 3 {
				attachment, err := images.Put(ctx, "conversation", callID, tooltypes.ToolAttachment{Type: "image"}, bytes.NewReader(content.Bytes()))
				if err != nil {
					return reply, err
				}
				reply.Attachments = append(reply.Attachments, attachment)
			}
			return reply, nil
		},
	})
	input, err := json.Marshal(map[string]string{"code": fmt.Sprintf(`
const reply = await tools.images({});
emit.artifact(reply.attachments[0]);
emit.image(reply.attachments[1]);
emit.image(reply.attachments[2]);
return {foreign: %q, keyOnly: {[reply.attachments[2].artifactId]: true},
  substring: "prefix " + reply.attachments[2].artifactId,
  encoded: JSON.stringify({id: reply.attachments[2].artifactId})};
`, foreign.ArtifactID)})
	require.NoError(t, err)
	execution, err := environment.ExecuteTool(ctx, agentenv.ToolRequest{Name: "code_execute", ToolCallID: "parent", Input: string(input)}, nil)
	require.NoError(t, err)
	require.NoError(t, <-hookDone)
	require.True(t, execution.StructuredResult.Success, execution.StructuredResult.Error)
	require.True(t, execution.Modified)
	require.Len(t, execution.StructuredResult.Attachments, 2, "hook-removed outputs and foreign references must not grant ownership")
	metadata := execution.StructuredResult.Metadata.(tooltypes.CodeExecutionMetadata)
	require.Len(t, metadata.Items, 3, "hooks cannot add, upgrade, or duplicate image emissions")
	parts := (tools.CodeExecuteResult{Metadata: metadata, Attachments: execution.StructuredResult.Attachments}).ContentParts()
	require.Len(t, parts, 5)
	assert.Equal(t, tooltypes.ToolResultContentPartTypeImage, parts[3].Type)
	assert.Equal(t, metadata.Items[1].ArtifactID, parts[3].ArtifactID)
	attachment := execution.StructuredResult.Attachments[0]
	assert.Equal(t, metadata.Items[0].ArtifactID, attachment.ArtifactID)
	parent := convtypes.NewConversationRecord("conversation")
	parent.Provider = "openai"
	parent.ToolResults["parent"] = execution.StructuredResult
	require.NoError(t, store.Save(t.Context(), parent))
	loaded, err := store.Load(t.Context(), parent.ID)
	require.NoError(t, err)
	assert.Equal(t, parent.ToolResults["parent"].Attachments, loaded.ToolResults["parent"].Attachments)
	assert.Equal(t, parent.ToolResults["parent"].Metadata, loaded.ToolResults["parent"].Metadata)
	fork := convtypes.ForkConversationRecord(loaded)
	require.NoError(t, store.SaveConversationFork(t.Context(), parent.ID, fork))
	_, _, err = images.Get(t.Context(), parent.ID, foreign.ArtifactID)
	assert.ErrorIs(t, err, artifacts.ErrNotFound, "literal IDs in output cannot grant foreign ownership")

	_, err = database.ExecContext(t.Context(), `UPDATE image_artifacts SET created_at = ?`, time.Now().UTC().Add(-48*time.Hour))
	require.NoError(t, err)
	require.NoError(t, images.Close())
	images, err = artifacts.Open(t.Context(), path)
	require.NoError(t, err)
	for _, conversationID := range []string{parent.ID, fork.ID} {
		for _, attachment := range execution.StructuredResult.Attachments {
			_, _, err = images.Get(t.Context(), conversationID, attachment.ArtifactID)
			require.NoError(t, err, "retained and viewed artifacts must survive restart and fork")
		}
	}
}
