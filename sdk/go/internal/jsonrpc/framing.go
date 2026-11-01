package jsonrpc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// MaxFrameSize bounds a single Content-Length frame.
const MaxFrameSize = 512 * 1024 * 1024

// ErrMissingContentLength is returned for a frame header without Content-Length.
var ErrMissingContentLength = errors.New("missing Content-Length header")

// FrameReader reads LSP-style Content-Length frames.
type FrameReader struct {
	reader *bufio.Reader
}

// NewFrameReader returns a reader for Content-Length framed payloads.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{reader: bufio.NewReaderSize(r, 64*1024)}
}

// Read returns the next payload. Header lines may end with CRLF or LF.
func (f *FrameReader) Read() ([]byte, error) {
	length := -1
	sawHeader := false
	for {
		line, err := f.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && !sawHeader && line == "" {
				return nil, io.EOF
			}
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			if !sawHeader {
				continue
			}
			break
		}
		sawHeader = true
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "content-length") {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid Content-Length header %q", strings.TrimSpace(value))
		}
		length = parsed
	}
	if length < 0 {
		return nil, ErrMissingContentLength
	}
	if length > MaxFrameSize {
		return nil, fmt.Errorf("frame of %d bytes exceeds the %d byte limit", length, MaxFrameSize)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(f.reader, payload); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return payload, nil
}

// FrameWriter writes Content-Length frames; it is safe for concurrent use.
type FrameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewFrameWriter returns a writer for Content-Length framed payloads.
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{writer: w}
}

// Write writes one frame.
func (f *FrameWriter) Write(payload []byte) error {
	var frame bytes.Buffer
	frame.Grow(len(payload) + 32)
	frame.WriteString("Content-Length: ")
	frame.WriteString(strconv.Itoa(len(payload)))
	frame.WriteString("\r\n\r\n")
	frame.Write(payload)
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.writer.Write(frame.Bytes())
	return err
}

// LineReader reads newline-delimited messages with a size limit.
type LineReader struct {
	reader *bufio.Reader
	limit  int
}

// NewLineReader returns a reader that rejects lines longer than limit bytes.
func NewLineReader(r io.Reader, limit int) *LineReader {
	return &LineReader{reader: bufio.NewReaderSize(r, 64*1024), limit: limit}
}

// Read returns the next line without its trailing CR/LF.
func (l *LineReader) Read() ([]byte, error) {
	var line []byte
	for {
		chunk, err := l.reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > l.limit+2 {
			return nil, fmt.Errorf("message exceeds %d byte limit", l.limit)
		}
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			break
		}
		return nil, err
	}
	line = bytes.TrimRight(line, "\r\n")
	if len(line) > l.limit {
		return nil, fmt.Errorf("message exceeds %d byte limit", l.limit)
	}
	return line, nil
}
