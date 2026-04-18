package osutil

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanonicalizePath(t *testing.T) {
	t.Run("always_cleans_path", func(t *testing.T) {
		assert.Equal(t, filepath.Clean("foo/../bar"), CanonicalizePath("foo/../bar"))
	})

	if runtime.GOOS != "darwin" {
		t.Run("non_darwin_returns_cleaned_path", func(t *testing.T) {
			assert.Equal(t, "/private/var/tmp/example", CanonicalizePath("/private/var/tmp/example"))
		})
		return
	}

	t.Run("normalizes_private_var", func(t *testing.T) {
		assert.Equal(t, "/var/folders/example", CanonicalizePath("/private/var/folders/example"))
	})

	t.Run("normalizes_private_tmp", func(t *testing.T) {
		assert.Equal(t, "/tmp/example.sock", CanonicalizePath("/private/tmp/example.sock"))
	})
}
