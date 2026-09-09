package osutil

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubBrowserCommand(t *testing.T, name string, args ...string) *string {
	t.Helper()
	previous := browserCommand
	t.Cleanup(func() { browserCommand = previous })
	var requested string
	browserCommand = func(url string) (*exec.Cmd, error) {
		requested = url
		return exec.Command(name, args...), nil
	}
	return &requested
}

func TestOpenBrowserWithinReportsLauncherExitFailure(t *testing.T) {
	requested := stubBrowserCommand(t, "false")

	confirmed, err := OpenBrowserWithin("http://localhost:1234?token=secret", 5*time.Second)
	require.Error(t, err)
	assert.False(t, confirmed)
	assert.Equal(t, "http://localhost:1234?token=secret", *requested)
}

func TestOpenBrowserWithinConfirmsImmediateSuccess(t *testing.T) {
	stubBrowserCommand(t, "true")

	confirmed, err := OpenBrowserWithin("http://localhost:1234", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, confirmed)
}

func TestOpenBrowserWithinReportsUnconfirmedLauncher(t *testing.T) {
	stubBrowserCommand(t, "sleep", "30")

	start := time.Now()
	confirmed, err := OpenBrowserWithin("http://localhost:1234", 50*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, confirmed)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestOpenBrowserStartsWithoutWaiting(t *testing.T) {
	stubBrowserCommand(t, "false")

	assert.NoError(t, OpenBrowser("http://localhost:1234"))
}
