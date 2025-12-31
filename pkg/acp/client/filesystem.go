package client

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
)

// ReadTextFileRequest parameters for fs/read_text_file
type ReadTextFileRequest struct {
	Path string `json:"path"`
}

// ReadTextFileResponse from fs/read_text_file
type ReadTextFileResponse struct {
	Text string `json:"text"`
}

// WriteTextFileRequest parameters for fs/write_text_file
type WriteTextFileRequest struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// ReadTextFile reads a text file from the client's file system.
// This delegates file reading to the client, which may apply additional
// security checks or path restrictions.
func (c *Client) ReadTextFile(ctx context.Context, path string) (string, error) {
	if !c.HasReadFileCapability() {
		return "", errors.New("client does not support reading files")
	}

	params := ReadTextFileRequest{Path: path}

	result, err := c.caller.CallClient(ctx, "fs/read_text_file", params)
	if err != nil {
		return "", errors.Wrap(err, "failed to read file from client")
	}

	var resp ReadTextFileResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", errors.Wrap(err, "failed to parse read file response")
	}

	return resp.Text, nil
}

// WriteTextFile writes a text file to the client's file system.
// This delegates file writing to the client, which may apply additional
// security checks or path restrictions.
func (c *Client) WriteTextFile(ctx context.Context, path, text string) error {
	if !c.HasWriteFileCapability() {
		return errors.New("client does not support writing files")
	}

	params := WriteTextFileRequest{
		Path: path,
		Text: text,
	}

	_, err := c.caller.CallClient(ctx, "fs/write_text_file", params)
	if err != nil {
		return errors.Wrap(err, "failed to write file to client")
	}

	return nil
}
