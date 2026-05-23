package auth

import (
	"net/http"
	"testing"
)

func setDefaultHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()

	originalClient := http.DefaultClient
	http.DefaultClient = client
	t.Cleanup(func() {
		http.DefaultClient = originalClient
	})
}
