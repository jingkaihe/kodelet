package delegation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildEventSuccessWireSemantics(t *testing.T) {
	for _, success := range []*bool{nil, new(false), new(true)} {
		event := Event{Sequence: 1, Kind: "tool-result", Input: `{"path":"file.go"}`, ToolOutput: "contents", Success: success, Error: "diagnostic"}
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		var wire map[string]any
		require.NoError(t, json.Unmarshal(raw, &wire))
		assert.Equal(t, event.Input, wire["input"])
		assert.Equal(t, event.ToolOutput, wire["toolOutput"])
		assert.Equal(t, event.Error, wire["error"])
		if success == nil {
			assert.NotContains(t, wire, "success")
		} else {
			assert.Equal(t, *success, wire["success"])
		}
		var decoded Event
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, event, decoded)
	}
}

func TestChildRequestContextModes(t *testing.T) {
	for _, mode := range []string{"", "fresh", "fork"} {
		request := Request{RequestID: "request", Profile: "agent", Message: "hello", ContextMode: mode}
		require.NoError(t, request.Validate())
		request.Resume = "child"
		if mode == "fork" {
			require.Error(t, request.Validate())
		} else {
			require.NoError(t, request.Validate())
		}
	}
	for _, raw := range []string{`{"contextMode":"other"}`, `{"resume":"../parent"}`, `{"sourceId":"parent"}`, `{"contextMode":null}`, `{"resume":null}`} {
		request := Request{RequestID: "request", Profile: "agent", Message: "hello"}
		err := Decode([]byte(raw), &request)
		if err == nil {
			err = request.Validate()
		}
		assert.Error(t, err, raw)
	}
}
