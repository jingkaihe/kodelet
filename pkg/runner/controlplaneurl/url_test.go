package controlplaneurl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeBaseCanonicalizesEquivalentURLs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "host case and default TLS port", input: " HTTPS://EXAMPLE.COM:443/base/ ", want: "https://example.com/base"},
		{name: "loopback and default HTTP port", input: "http://LOCALHOST:080/", want: "http://localhost"},
		{name: "path cleanup and unreserved escapes", input: "https://example.com//%62ase/./child/../", want: "https://example.com/base"},
		{name: "nondefault port and path case", input: "https://Example.com:8443/Base", want: "https://example.com:8443/Base"},
		{name: "IPv6 normalization", input: "https://[0:0:0:0:0:0:0:1]:443/%62ase", want: "https://[::1]/base"},
		{name: "encoded reserved slash preserved", input: "https://example.com/base%2fchild", want: "https://example.com/base%2Fchild"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeBase(test.input)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)

			second, err := NormalizeBase(got)
			require.NoError(t, err)
			assert.Equal(t, got, second)
		})
	}
}

func TestNormalizeBaseRejectsUnsafeURLs(t *testing.T) {
	for _, input := range []string{
		"",
		"ftp://localhost",
		"http://example.com",
		"https://user@example.com",
		"https://example.com?token=secret",
		"https://example.com/#fragment",
		"https://example.com:70000",
	} {
		_, err := NormalizeBase(input)
		assert.Error(t, err, input)
	}
}

func TestEndpointEscapesDynamicSegmentsAndBuildsWebSocketURL(t *testing.T) {
	endpoint, err := Endpoint("https://EXAMPLE.com:443/base/", "api", "conversations", "conversation/one", "stop")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/base/api/conversations/conversation%2Fone/stop", endpoint)

	websocketURL, err := WebSocketEndpoint("http://LOCALHOST:80/base", "api", "runner", "v1", "connect")
	require.NoError(t, err)
	assert.Equal(t, "ws://localhost/base/api/runner/v1/connect", websocketURL)
}

func TestIsLoopbackHostname(t *testing.T) {
	assert.True(t, IsLoopbackHostname("LOCALHOST."))
	assert.True(t, IsLoopbackHostname("127.0.0.1"))
	assert.True(t, IsLoopbackHostname("::1"))
	assert.False(t, IsLoopbackHostname("kodelet.example"))
}
