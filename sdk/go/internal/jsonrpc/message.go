// Package jsonrpc contains the JSON-RPC 2.0 message model and stream framing
// shared by the extension runtime and the ACP client.
package jsonrpc

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Version is the only JSON-RPC version Kodelet speaks.
const Version = "2.0"

// Standard JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidParams  = -32602
	CodeMethodNotFound = -32601
	CodeServerError    = -32000
)

// Message is a JSON-RPC request, notification, or response. Raw fields keep
// IDs and payloads byte-for-byte so they can be echoed or relayed unchanged.
type Message struct {
	JSONRPC  string          `json:"jsonrpc"`
	ID       json.RawMessage `json:"id,omitempty"`
	ParentID json.RawMessage `json:"parentId,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object. It implements the error interface so a
// handler can return one to control the code sent back to the peer.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return e.Message
}

var nullJSON = json.RawMessage("null")

// HasID reports whether the message carries a non-null ID.
func (m *Message) HasID() bool {
	return HasValue(m.ID)
}

// IsRequest reports whether the message is a request that expects a response.
func (m *Message) IsRequest() bool {
	return m.Method != "" && m.HasID()
}

// HasValue reports whether raw contains a JSON value other than null.
func HasValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, nullJSON)
}

// IntID returns a numeric message ID, if it is one.
func (m *Message) IntID() (int64, bool) {
	if !m.HasID() {
		return 0, false
	}
	value, err := strconv.ParseInt(string(bytes.TrimSpace(m.ID)), 10, 64)
	return value, err == nil
}

// IDKey returns a canonical map key for a number or string ID, so that 1 and
// "1" remain distinct. The boolean is false for absent, null, or invalid IDs.
func IDKey(raw json.RawMessage) (string, bool) {
	if !HasValue(raw) {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	switch typed := value.(type) {
	case json.Number:
		return "n:" + typed.String(), true
	case string:
		return "s:" + typed, true
	default:
		return "", false
	}
}

// IntRaw encodes an integer ID.
func IntRaw(id int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(id, 10))
}

// Marshal encodes a value, returning nil for nil so the field is omitted.
func Marshal(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	if raw, ok := value.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(value)
}

// NewRequest builds a request message.
func NewRequest(id json.RawMessage, method string, params json.RawMessage) Message {
	return Message{JSONRPC: Version, ID: id, Method: method, Params: params}
}

// NewNotification builds a notification message.
func NewNotification(method string, params json.RawMessage) Message {
	return Message{JSONRPC: Version, Method: method, Params: params}
}

// NewResult builds a success response; a nil result is encoded as null.
func NewResult(id json.RawMessage, result json.RawMessage) Message {
	if len(result) == 0 {
		result = nullJSON
	}
	if len(id) == 0 {
		id = nullJSON
	}
	return Message{JSONRPC: Version, ID: id, Result: result}
}

// NewError builds an error response.
func NewError(id json.RawMessage, code int, message string) Message {
	if len(id) == 0 {
		id = nullJSON
	}
	return Message{JSONRPC: Version, ID: id, Error: &Error{Code: code, Message: message}}
}
