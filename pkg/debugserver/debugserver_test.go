package debugserver

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "disabled", config: Config{}},
		{name: "ipv4 loopback", config: Config{Address: "127.0.0.1:6060"}},
		{name: "ipv6 loopback", config: Config{Address: "[::1]:6060"}},
		{name: "localhost", config: Config{Address: "localhost:6060"}},
		{name: "ephemeral port", config: Config{Address: " 127.0.0.1:0 "}},
		{name: "all interfaces", config: Config{Address: ":6060"}, wantErr: "not a loopback address"},
		{name: "ipv4 wildcard", config: Config{Address: "0.0.0.0:6060"}, wantErr: "not a loopback address"},
		{name: "ipv6 wildcard", config: Config{Address: "[::]:6060"}, wantErr: "not a loopback address"},
		{name: "private address", config: Config{Address: "10.0.0.5:6060"}, wantErr: "not a loopback address"},
		{name: "hostname", config: Config{Address: "kodelet.internal:6060"}, wantErr: "not a loopback address"},
		{name: "remote explicitly allowed", config: Config{Address: "0.0.0.0:6060", AllowRemote: true}},
		{name: "missing port", config: Config{Address: "127.0.0.1"}, wantErr: "use host:port"},
		{name: "port out of range", config: Config{Address: "127.0.0.1:70000"}, wantErr: "between 0 and 65535"},
		{name: "named port", config: Config{Address: "127.0.0.1:http"}, wantErr: "between 0 and 65535"},
		{name: "remote without address", config: Config{AllowRemote: true}, wantErr: "requires --debug-addr"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestStartRejectsInvalidConfigBeforeBinding(t *testing.T) {
	_, err := Start(t.Context(), Config{})
	require.ErrorContains(t, err, "debug address is required")
	_, err = Start(t.Context(), Config{Address: "0.0.0.0:0"})
	require.ErrorContains(t, err, "not a loopback address")
}

func TestServerServesProfilesWithoutCmdline(t *testing.T) {
	server := startTestServer(t)
	assert.True(t, server.Loopback())
	base := "http://" + server.Addr().String()

	status, body := get(t, base+"/debug/pprof/")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "goroutine")

	// Frames are symbolized in-process.
	status, body = get(t, base+"/debug/pprof/goroutine?debug=1")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "debugserver.TestServerServesProfilesWithoutCmdline")

	status, body = get(t, base+"/debug/pprof/cmdline")
	assert.Equal(t, http.StatusNotFound, status)
	assert.Contains(t, body, "can contain credentials")
	assert.NotContains(t, body, os.Args[0])

	for _, path := range []string{"/debug/pprof/heap", "/debug/pprof/allocs", "/debug/pprof/profile?seconds=1", "/debug/pprof/trace?seconds=1"} {
		status, body = get(t, base+path)
		assert.Equal(t, http.StatusOK, status, path)
		assert.NotEmpty(t, body, path)
	}

	status, body = get(t, base+"/debug/pprof/symbol")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "num_symbols")

	response, err := http.Post(base+"/debug/pprof/cmdline", "text/plain", strings.NewReader(""))
	require.NoError(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
}

func TestCloseAbortsInFlightProfiles(t *testing.T) {
	server, err := Start(t.Context(), Config{Address: "127.0.0.1:0"})
	require.NoError(t, err)
	base := "http://" + server.Addr().String()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		client := &http.Client{Timeout: 30 * time.Second}
		response, err := client.Get(base + "/debug/pprof/profile?seconds=30")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	require.Eventually(t, func() bool {
		_, body := get(t, base+"/debug/pprof/goroutine?debug=1")
		return strings.Contains(body, "net/http/pprof.Profile")
	}, 5*time.Second, 20*time.Millisecond, "the CPU profile request should be in flight")

	started := time.Now()
	require.NoError(t, server.Close())
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		require.Fail(t, "an in-flight CPU profile kept running after Close")
	}
	assert.Less(t, time.Since(started), 5*time.Second)

	_, err = http.Get(base + "/debug/pprof/")
	assert.Error(t, err, "the listener must stop accepting connections")
}

func startTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := Start(t.Context(), Config{Address: "127.0.0.1:0"})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, server.Close()) })
	return server
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	response, err := http.Get(url)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(body)
}
