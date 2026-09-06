package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildHostFencesLeasesAndNeverFallsBack(t *testing.T) {
	for _, scenario := range []string{"no peer", "provisional", "wrong owner", "wrong run", "absent", "no active tool", "delegate", "unknown options"} {
		t.Run(scenario, func(t *testing.T) {
			owner := extensions.UIExtensionOwner{ExtensionID: "search", Generation: 7}
			cwd := t.TempDir()
			resources := &runnerBackgroundResources{workingDirectory: cwd, runIDs: map[string]struct{}{"parent": {}}, leases: map[string]runnerBackgroundLease{"lease": {owner: owner}}}
			s := &Service{runs: map[string]*activeRun{"parent": {manifest: runnerpayload.Manifest{WorkingDirectory: cwd}}}, backgroundLeases: map[string]*runnerBackgroundResources{"lease": resources}}
			ctx := s.decorateRunContext(t.Context(), "parent", "conversation")
			ctx = context.WithValue(ctx, childToolKey{}, "tool")
			raw := json.RawMessage(`{"requestId":"one","profile":"search","message":"search","leaseId":"lease"}`)
			switch scenario {
			case "provisional":
				resources.leases["lease"] = runnerBackgroundLease{owner: owner, openingRunID: "parent"}
			case "wrong owner":
				owner.Generation++
			case "wrong run":
				delete(resources.runIDs, "parent")
			case "absent":
				delete(s.backgroundLeases, "lease")
			case "no active tool":
				raw = []byte(`{"requestId":"one","profile":"search","message":"search"}`)
				ctx = s.decorateRunContext(t.Context(), "parent", "conversation")
			case "unknown options":
				raw = []byte(`{"requestId":"one","profile":"search","message":"search","options":{"apiKey":"no"}}`)
			case "delegate":
				s.peer = &modelHelperPeer{call: func(_ context.Context, method string, params, result any) error {
					assert.Equal(t, delegation.StartMethod, method)
					p := params.(delegation.Params)
					assert.Equal(t, "parent", p.RunID)
					assert.Equal(t, "tool", p.ToolCallID)
					assert.Equal(t, uint64(7), p.Generation)
					assert.Equal(t, cwd, p.Request.CWD)
					*result.(*delegation.Result) = delegation.Result{Identity: delegation.Identity{ConversationID: "child"}}
					return nil
				}}
			}
			_, err := s.ChildRequest(ctx, &recordingUIExtensionSource{owner: owner}, delegation.StartMethod, raw)
			if scenario == "delegate" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario == "provisional" {
				assert.ErrorContains(t, err, "provisional")
			}
			if scenario == "no peer" {
				assert.ErrorContains(t, err, "central child execution is unavailable")
			}
		})
	}
}
