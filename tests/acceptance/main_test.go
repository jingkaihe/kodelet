package acceptance

import (
	"os"
	"testing"
)

// TestMain runs setup and teardown for acceptance tests
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func commandEnv() []string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	return []string{
		"HOME=" + home,
		"PATH=" + path,
	}
}
