package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIngestAttachmentsAuthorizesNestedReferencesWithChildIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		nested bool
		denied bool
	}{
		{name: "nested authorized", nested: true},
		{name: "nested foreign", nested: true, denied: true},
		{name: "direct normalized centrally"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			original := tooltypes.ToolAttachment{Type: "image", ArtifactID: "requested-id", ShortCode: "untrusted", Filename: "untrusted.png"}
			authorized := tooltypes.ToolAttachment{Type: "image", ArtifactID: "requested-id", ShortCode: "host-code", Filename: "host.png"}
			peer := &modelHelperPeer{call: func(_ context.Context, method string, params, result any) error {
				calls++
				assert.Equal(t, runnerpayload.MethodArtifactResolve, method)
				assert.Equal(t, runnerpayload.ArtifactRequest{RunID: "run", ToolCallID: "child", ArtifactID: original.ArtifactID}, params)
				if test.denied {
					return errors.New("artifact is not in this conversation")
				}
				*result.(*tooltypes.ToolAttachment) = authorized
				return nil
			}}
			ctx := contextWithRunnerArtifactResolver(t.Context(), peer, "run", "child")
			wire := runnerpayload.ToolResult{Structured: tooltypes.StructuredToolResult{Attachments: []tooltypes.ToolAttachment{original}}}
			(&Service{}).ingestAttachments(ctx, peer, &activeRun{}, "child", &wire, test.nested)
			require.Len(t, wire.Structured.Attachments, 1)
			got := wire.Structured.Attachments[0]
			switch {
			case !test.nested:
				assert.Zero(t, calls, "direct results already have a central normalization boundary")
				assert.Equal(t, original, got)
			case test.denied:
				assert.Equal(t, 1, calls)
				assert.Empty(t, got.ArtifactID, "foreign references must not enter the parent inventory")
				assert.Empty(t, got.ShortCode)
				assert.Contains(t, got.Error, "not in this conversation")
			default:
				assert.Equal(t, 1, calls)
				assert.Equal(t, authorized, got, "use the host descriptor, not child-supplied fields")
			}
		})
	}
}

func TestIngestAttachmentsUploadsSourcesWithoutResolvingAgain(t *testing.T) {
	for _, test := range []struct {
		name   string
		inline bool
		count  int
		status int
	}{
		{name: "local path", count: 1, status: http.StatusCreated},
		{name: "inline image", inline: true, count: 1, status: http.StatusCreated},
		{name: "inline upload rejected", inline: true, count: 1, status: http.StatusBadRequest},
		{name: "inline image count bounded", inline: true, count: 9, status: http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := []byte("image bytes validated by the control plane")
			attachment := tooltypes.ToolAttachment{Type: "image", ArtifactID: "saved", ShortCode: "saved-code"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer upload-token", r.Header.Get("Authorization"))
				assert.Equal(t, runnerpayload.ArtifactUploadPath, r.URL.Path)
				data, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, image, data, "only decoded bytes travel over the artifact upload channel")
				w.WriteHeader(test.status)
				assert.NoError(t, json.NewEncoder(w).Encode(attachment))
			}))
			t.Cleanup(server.Close)
			workspace := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "image.png"), image, 0o600))
			calls := 0
			peer := &modelHelperPeer{call: func(_ context.Context, method string, params, result any) error {
				calls++
				assert.Equal(t, runnerpayload.MethodArtifactUpload, method)
				request := params.(runnerpayload.ArtifactRequest)
				assert.Equal(t, "child", request.ToolCallID)
				assert.Equal(t, "run", request.RunID)
				assert.Empty(t, request.Attachment.Data, "RPC carries metadata only, not image bytes")
				assert.Empty(t, request.Attachment.Path)
				*result.(*runnerpayload.ArtifactUploadGrant) = runnerpayload.ArtifactUploadGrant{Token: "upload-token"}
				return nil
			}}
			ctx := contextWithRunnerArtifactResolver(t.Context(), peer, "run", "child")
			run := &activeRun{id: "run", manifest: runnerpayload.Manifest{WorkingDirectory: workspace}}
			wire := runnerpayload.ToolResult{}
			for range test.count {
				source := tooltypes.ToolAttachment{Type: "image", Path: "image.png"}
				if test.inline {
					source.Path, source.Data = "", base64.StdEncoding.EncodeToString(image)
				}
				wire.Structured.Attachments = append(wire.Structured.Attachments, source)
			}
			(&Service{artifactBaseURL: server.URL}).ingestAttachments(ctx, peer, run, "child", &wire, true)
			assert.Equal(t, min(test.count, runnerpayload.MaxToolAttachments), calls, "new uploads need no extra resolve call")
			require.Len(t, wire.Structured.Attachments, test.count)
			for i, got := range wire.Structured.Attachments {
				assert.Empty(t, got.Data)
				assert.Empty(t, got.Path)
				if test.status != http.StatusCreated || i >= runnerpayload.MaxToolAttachments {
					assert.NotEmpty(t, got.Error)
					assert.Empty(t, got.ArtifactID)
				} else {
					assert.Equal(t, attachment, got)
				}
			}
		})
	}
}

func TestIngestAttachmentsRejectsInvalidInlineSourcesAndClearsData(t *testing.T) {
	for _, test := range []struct {
		name       string
		attachment tooltypes.ToolAttachment
		dataSize   int
	}{
		{name: "missing source"},
		{name: "invalid base64", attachment: tooltypes.ToolAttachment{Data: "not-base64"}},
		{name: "nonzero padding bits", attachment: tooltypes.ToolAttachment{Data: "Zh=="}},
		{name: "newlines", attachment: tooltypes.ToolAttachment{Data: "Zg==\n"}},
		{name: "data URL", attachment: tooltypes.ToolAttachment{Data: "data:image/png;base64,Zg=="}},
		{name: "path and data", attachment: tooltypes.ToolAttachment{Path: "image.png", Data: "Zg=="}},
		{name: "reference and data", attachment: tooltypes.ToolAttachment{ArtifactID: "forged", ShortCode: "forged", ViewURL: "forged", Data: "Zg=="}},
		{name: "preexisting error", attachment: tooltypes.ToolAttachment{Path: "image.png", Data: "Zg==", Error: "tool could not finish"}},
		{name: "encoded length oversized", dataSize: base64.StdEncoding.EncodedLen(runnerpayload.MaxArtifactBytes) + 4},
		{name: "decoded length oversized", dataSize: base64.StdEncoding.EncodedLen(runnerpayload.MaxArtifactBytes)},
	} {
		t.Run(test.name, func(t *testing.T) {
			attachment := test.attachment
			attachment.Type = "image"
			if test.dataSize > 0 {
				attachment.Data = strings.Repeat("A", test.dataSize)
			}
			peer := &modelHelperPeer{call: func(context.Context, string, any, any) error {
				t.Error("invalid sources must not request upload or reference authorization")
				return errors.New("unexpected call")
			}}
			wire := runnerpayload.ToolResult{Structured: tooltypes.StructuredToolResult{
				Attachments: []tooltypes.ToolAttachment{attachment},
			}}
			(&Service{artifactBaseURL: "http://unused"}).ingestAttachments(t.Context(), peer, &activeRun{}, "child", &wire, false)
			require.Len(t, wire.Structured.Attachments, 1)
			got := wire.Structured.Attachments[0]
			assert.NotEmpty(t, got.Error)
			assert.Empty(t, got.Data)
			assert.Empty(t, got.Path)
			assert.Empty(t, got.ArtifactID)
			assert.Empty(t, got.ShortCode)
			assert.Empty(t, got.ViewURL)
		})
	}
}
