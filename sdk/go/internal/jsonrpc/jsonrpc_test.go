package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameRoundTripAcceptsLFHeadersAndRejectsMissingLength(t *testing.T) {
	var buffer bytes.Buffer
	writer := NewFrameWriter(&buffer)
	require.NoError(t, writer.Write([]byte(`{"a":1}`)))
	buffer.WriteString("Content-Type: application/json\nContent-Length: 2\n\n{}")
	buffer.WriteString("X-Other: 1\r\n\r\n{}")
	reader := NewFrameReader(&buffer)
	payload, err := reader.Read()
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(payload))
	payload, err = reader.Read()
	require.NoError(t, err)
	assert.Equal(t, "{}", string(payload))
	_, err = reader.Read()
	require.ErrorIs(t, err, ErrMissingContentLength)

	_, err = NewFrameReader(strings.NewReader("")).Read()
	require.ErrorIs(t, err, io.EOF)
	_, err = NewFrameReader(strings.NewReader("Content-Length: 10\r\n\r\n{}")).Read()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	_, err = NewFrameReader(strings.NewReader("Content-Length: x\r\n\r\n")).Read()
	require.Error(t, err)
}

func TestLineReaderTrimsCRAndEnforcesLimit(t *testing.T) {
	reader := NewLineReader(strings.NewReader("one\r\ntwo\nlast"), 8)
	for _, expected := range []string{"one", "two", "last"} {
		line, err := reader.Read()
		require.NoError(t, err)
		assert.Equal(t, expected, string(line))
	}
	_, err := reader.Read()
	require.True(t, errors.Is(err, io.EOF))
	_, err = NewLineReader(strings.NewReader(strings.Repeat("x", 100)+"\n"), 8).Read()
	require.ErrorContains(t, err, "exceeds 8 byte limit")
}

func TestMessageIDsAndResponses(t *testing.T) {
	number, ok := IDKey(json.RawMessage("1"))
	require.True(t, ok)
	text, ok := IDKey(json.RawMessage(`"1"`))
	require.True(t, ok)
	assert.NotEqual(t, number, text)
	_, ok = IDKey(json.RawMessage("null"))
	assert.False(t, ok)
	_, ok = IDKey(json.RawMessage("{}"))
	assert.False(t, ok)

	data, err := json.Marshal(NewResult(IntRaw(3), nil))
	require.NoError(t, err)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":3,"result":null}`, string(data))
	data, err = json.Marshal(NewError(nil, CodeParseError, "bad"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"bad"}}`, string(data))
	var message Message
	require.NoError(t, json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":7,"method":"m"}`), &message))
	assert.True(t, message.IsRequest())
	id, ok := message.IntID()
	assert.True(t, ok)
	assert.Equal(t, int64(7), id)
}
