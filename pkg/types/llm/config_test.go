package llm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBashConfigMarshalJSONUsesDurationString(t *testing.T) {
	data, err := json.Marshal(BashConfig{Timeout: 5 * time.Minute})
	require.NoError(t, err)

	assert.JSONEq(t, `{"timeout":"5m0s"}`, string(data))
}

func TestBashConfigMarshalYAMLUsesDurationString(t *testing.T) {
	data, err := yaml.Marshal(BashConfig{Timeout: 5 * time.Minute})
	require.NoError(t, err)

	assert.Equal(t, "timeout: 5m0s\n", string(data))
}
