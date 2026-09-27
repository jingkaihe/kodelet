package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/pkg/errors"
)

const maxServerStatusResponseSize = 64 << 10

// ServerStatus is the connected server's version, build metadata, and readiness.
type ServerStatus struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	APIReady  bool   `json:"apiReady"`
}

// ServerStatus reports the connected server's version and build metadata.
func (r *Client) ServerStatus(ctx context.Context) (ServerStatus, error) {
	var result ServerStatus
	if r == nil || r.client == nil {
		return result, errors.New("the chat connection is not initialized")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "status")
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, errors.Wrap(err, "failed to create server status request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "could not contact the server")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxServerStatusResponseSize)).Decode(&result); err != nil {
		return result, errors.Wrap(err, "invalid server status response")
	}
	return result, nil
}
