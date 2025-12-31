// Package client provides the agent-to-client RPC interface for ACP.
// This enables the agent to make requests back to the client, such as
// requesting permissions, reading/writing files, or executing terminal commands.
package client

import (
	"context"
	"encoding/json"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
)

// RPCCaller is the interface for making RPC calls to the client
type RPCCaller interface {
	CallClient(ctx context.Context, method string, params any) (json.RawMessage, error)
	GetClientCapabilities() *acptypes.ClientCapabilities
}

// Client wraps an RPCCaller to provide typed methods for agent→client calls
type Client struct {
	caller RPCCaller
}

// New creates a new Client with the given RPC caller
func New(caller RPCCaller) *Client {
	return &Client{caller: caller}
}

// HasFSCapability checks if the client has file system capabilities
func (c *Client) HasFSCapability() bool {
	caps := c.caller.GetClientCapabilities()
	return caps != nil && caps.FS != nil
}

// HasReadFileCapability checks if the client can read files
func (c *Client) HasReadFileCapability() bool {
	caps := c.caller.GetClientCapabilities()
	return caps != nil && caps.FS != nil && caps.FS.ReadTextFile
}

// HasWriteFileCapability checks if the client can write files
func (c *Client) HasWriteFileCapability() bool {
	caps := c.caller.GetClientCapabilities()
	return caps != nil && caps.FS != nil && caps.FS.WriteTextFile
}

// HasTerminalCapability checks if the client has terminal capabilities
func (c *Client) HasTerminalCapability() bool {
	caps := c.caller.GetClientCapabilities()
	return caps != nil && caps.Terminal
}
