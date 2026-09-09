package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionOutputFormats(t *testing.T) {
	var output bytes.Buffer
	ext := ExtensionOutput{ID: "weather", Name: "Weather", Source: "local_standalone", Path: "/runner/weather", Directory: "/runner", PluginRef: "org@repo"}
	require.NoError(t, renderExtensionInspectJSON(&output, ext))
	var decoded ExtensionOutput
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	assert.Equal(t, ext, decoded)
	output.Reset()
	require.NoError(t, renderExtensionInspectTable(&output, ext))
	assert.Contains(t, output.String(), "/runner/weather")
	assert.Contains(t, output.String(), "org@repo")
	output.Reset()
	require.NoError(t, (&ExtensionListOutput{Extensions: []ExtensionOutput{}, Format: JSONFormat}).Render(&output))
	assert.JSONEq(t, `{"extensions":[]}`, output.String())
	output.Reset()
	require.NoError(t, (&ExtensionListOutput{Extensions: []ExtensionOutput{ext}, Format: TableFormat}).Render(&output))
	assert.Contains(t, output.String(), "weather")
}
