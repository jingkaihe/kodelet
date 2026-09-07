package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionExtensionFramesArePinnedToRunAndConnection(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registrationParams := testRegisterParams("host", "/workspace")
	registrationParams.Capabilities.SessionExtensions = true
	registration, err := registry.Register(registrationParams, link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	identity := UIRequestIdentity{registration.RunnerID, registration.ConnectionID, registration.Generation}
	frame := protocol.ExtensionFrame{AttachmentID: "attachment", RunID: "run", ExtensionID: "inline-1", Message: json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	params := testRunOpenParams("run", "conversation")
	params.SessionExtensions = &protocol.SessionExtensions{ID: frame.AttachmentID, ExtensionIDs: []string{frame.ExtensionID}}
	var delivered int
	link.call = func(_ context.Context, method string, value any, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			// Initialization replies must be deliverable before openRun returns.
			run, err := registry.ValidateSessionExtensionFrame(identity, frame)
			require.NoError(t, err)
			assert.Equal(t, RunStatusOpening, run.Status)
			require.NoError(t, registry.DeliverSessionExtensionFrame(t.Context(), identity, frame))
			manifest := runnerpayload.Manifest{
				ProtocolVersion: protocol.Version, RunnerID: registration.RunnerID,
				RunID: "run", Generation: registration.Generation, WorkingDirectory: "/workspace", SessionExtensionIDs: []string{"inline-1"},
			}
			manifest.Digest, err = runnerpayload.ComputeManifestDigest(manifest)
			require.NoError(t, err)
			*result.(*runnerpayload.Manifest) = manifest
		case protocol.MethodSessionExtensionFrame:
			assert.Equal(t, frame, value)
			delivered++
		}
		return nil
	}
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, params)
	require.NoError(t, err)
	assert.Equal(t, 1, delivered)
	// Mutating caller-owned descriptors cannot widen a pinned run.
	params.SessionExtensions.ExtensionIDs[0] = "inline-2"
	params.SessionExtensions.ID = "replacement"
	require.NoError(t, registry.DeliverSessionExtensionFrame(t.Context(), identity, frame))
	for _, mutate := range []func(*UIRequestIdentity, *protocol.ExtensionFrame){
		func(i *UIRequestIdentity, _ *protocol.ExtensionFrame) { i.RunnerID = "other" },
		func(i *UIRequestIdentity, _ *protocol.ExtensionFrame) { i.ConnectionID = "other" },
		func(i *UIRequestIdentity, _ *protocol.ExtensionFrame) { i.Generation++ },
		func(_ *UIRequestIdentity, f *protocol.ExtensionFrame) { f.RunID = "other" },
		func(_ *UIRequestIdentity, f *protocol.ExtensionFrame) { f.AttachmentID = "replacement" },
		func(_ *UIRequestIdentity, f *protocol.ExtensionFrame) { f.ExtensionID = "inline-2" },
	} {
		otherIdentity, otherFrame := identity, frame
		mutate(&otherIdentity, &otherFrame)
		assert.Error(t, registry.DeliverSessionExtensionFrame(t.Context(), otherIdentity, otherFrame))
	}
	assert.Equal(t, 2, delivered, "rejected frames never reach the runner")
	require.NoError(t, registry.CancelRun(t.Context(), "run", "test"))
	require.NoError(t, registry.DeliverSessionExtensionFrame(t.Context(), identity, frame), "bounded shutdown replies remain routable")
	require.NoError(t, registry.CloseRun(t.Context(), "run", RunStatusCanceled, nil))
	assert.Error(t, registry.DeliverSessionExtensionFrame(t.Context(), identity, frame))
	ids, err := registry.RequiredSessionExtensions("conversation")
	require.NoError(t, err)
	assert.Equal(t, []string{"inline-1"}, ids)
	run, exists := registry.Run("run")
	require.True(t, exists)
	assert.NotContains(t, run.ManifestJSON, "attachment", "live attachment capability is not persisted")
}

func TestSessionExtensionsRequireRunnerCapabilityAndManifestAgreement(t *testing.T) {
	registry := newTestRegistry(t)
	link := newFakeLink()
	registration, err := registry.Register(testRegisterParams("host", "/workspace"), link)
	require.NoError(t, err)
	markRunnerReady(t, registry, registration)
	params := testRunOpenParams("run", "conversation")
	params.SessionExtensions = &protocol.SessionExtensions{ID: "attachment", ExtensionIDs: []string{"inline-1"}}
	_, err = registry.OpenRun(t.Context(), registration.RunnerID, params)
	assert.ErrorContains(t, err, "does not support session extensions")
	assert.ErrorContains(t, validateManifest(runnerpayload.Manifest{}, registration.RunnerID, params, registration.Generation), "session extensions do not match")
}

func TestSessionExtensionRequirementsSurviveRegistryRestart(t *testing.T) {
	manifest := runnerpayload.Manifest{SessionExtensionIDs: []string{"inline-1", "inline-2"}}
	snapshot := string(mustRegistryJSON(t, manifest))
	persistence := &testPersistence{state: PersistedState{Runners: []Runner{{ID: "runner", Host: protocol.Host{InstanceID: "host"}, Workspace: protocol.Workspace{Path: "/workspace"}}}, Runs: []Run{
		{ID: "old", RunnerID: "runner", ConversationID: "conversation", Status: RunStatusSucceeded, ManifestJSON: snapshot, CreatedAt: time.Unix(1, 0)},
		{ID: "failed-open", RunnerID: "runner", ConversationID: "conversation", Status: RunStatusFailed, CreatedAt: time.Unix(2, 0)},
	}}}
	registry, err := New(t.Context(), Options{Persistence: persistence})
	require.NoError(t, err)
	t.Cleanup(func() { _ = registry.Close() })
	ids, err := registry.RequiredSessionExtensions("conversation")
	require.NoError(t, err)
	assert.Equal(t, []string{"inline-1", "inline-2"}, ids)
	ids, err = registry.RequiredSessionExtensions("other")
	require.NoError(t, err)
	assert.Empty(t, ids)
}
