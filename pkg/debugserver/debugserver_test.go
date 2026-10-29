package debugserver

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAddress(t *testing.T) {
	for address, wantErr := range map[string]string{
		"127.0.0.1:6060":  "",
		"0.0.0.0:6060":    "",
		":6060":           "has no host",
		"127.0.0.1":       "use host:port",
		"127.0.0.1:70000": "between 0 and 65535",
	} {
		err := ValidateAddress(address)
		if wantErr == "" {
			assert.NoError(t, err, address)
		} else {
			assert.ErrorContains(t, err, wantErr, address)
		}
	}
}

func TestIndexURL(t *testing.T) {
	for ip, want := range map[string]string{
		"127.0.0.1": "http://127.0.0.1:6060/debug/pprof/",
		"0.0.0.0":   "http://localhost:6060/debug/pprof/",
		"::":        "http://localhost:6060/debug/pprof/",
	} {
		assert.Equal(t, want, indexURL(&net.TCPAddr{IP: net.ParseIP(ip), Port: 6060}), ip)
	}
}

func TestServer(t *testing.T) {
	server, err := Start(t.Context(), "127.0.0.1:0")
	require.NoError(t, err)
	base := "http://" + server.Addr().String()
	assert.True(t, server.Loopback())

	assert.Equal(t, http.StatusOK, get(t, base+"/debug/pprof/goroutine?debug=1"))
	assert.Equal(t, http.StatusNotFound, get(t, base+"/debug/pprof/cmdline"))

	// Close must abort an in-flight CPU profile rather than wait for it.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if response, err := http.Get(base + "/debug/pprof/profile?seconds=30"); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	require.Eventually(t, func() bool {
		response, err := http.Get(base + "/debug/pprof/goroutine?debug=1")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return strings.Contains(string(body), "net/http/pprof.Profile")
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, server.Close())
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		require.Fail(t, "Close did not abort the in-flight profile")
	}
}

func get(t *testing.T, url string) int {
	t.Helper()
	response, err := http.Get(url)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode
}
