package delegation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
