package extensions

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

const (
	UIInputStatusSubmitted   = "submitted"
	UIInputStatusDismissed   = "dismissed"
	UIInputStatusTimeout     = "timeout"
	UIInputStatusUnavailable = "unavailable"
)

// UIInputRequest describes a user-input prompt requested by an extension.
type UIInputRequest struct {
	ID               string `json:"id,omitempty"`
	Title            string `json:"title"`
	HelpText         string `json:"helpText,omitempty"`
	Placeholder      string `json:"placeholder,omitempty"`
	DefaultValue     string `json:"defaultValue,omitempty"`
	SubmitButtonText string `json:"submitButtonText,omitempty"`
	CancelButtonText string `json:"cancelButtonText,omitempty"`
	Required         bool   `json:"required,omitempty"`
	Secret           bool   `json:"secret,omitempty"`
}

// UIInputResponse is returned to the extension after a UI input prompt resolves.
type UIInputResponse struct {
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// UIInputBroker routes extension UI input requests to the active user interface.
type UIInputBroker interface {
	Input(ctx context.Context, request UIInputRequest) (UIInputResponse, error)
}

type uiInputBrokerKey struct{}

// ContextWithUIInputBroker attaches a UI input broker to the active run context.
func ContextWithUIInputBroker(ctx context.Context, broker UIInputBroker) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if broker == nil {
		return ctx
	}
	return context.WithValue(ctx, uiInputBrokerKey{}, broker)
}

// UIInputBrokerFromContext returns the run-scoped UI input broker, if one exists.
func UIInputBrokerFromContext(ctx context.Context) (UIInputBroker, bool) {
	if ctx == nil {
		return nil, false
	}
	broker, ok := ctx.Value(uiInputBrokerKey{}).(UIInputBroker)
	return broker, ok && broker != nil
}

// NewUIInputRequestID returns a unique request ID for UI input prompts.
func NewUIInputRequestID() string {
	return conversations.GenerateID()
}

// TerminalUIInputBroker prompts for extension-requested input in an interactive terminal.
type TerminalUIInputBroker struct {
	In          io.Reader
	Out         io.Writer
	Interactive bool

	mu     sync.Mutex
	reader *bufio.Reader
}

// NewTerminalUIInputBroker creates a terminal-backed UI input broker.
func NewTerminalUIInputBroker(in io.Reader, out io.Writer) *TerminalUIInputBroker {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stderr
	}
	return &TerminalUIInputBroker{
		In:          in,
		Out:         out,
		Interactive: readerIsTerminal(in),
		reader:      bufio.NewReader(in),
	}
}

// Input asks the user for a single line of input.
func (b *TerminalUIInputBroker) Input(ctx context.Context, request UIInputRequest) (UIInputResponse, error) {
	if b == nil || !b.Interactive {
		return UIInputResponse{Status: UIInputStatusUnavailable, Reason: "terminal input is not available"}, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return UIInputResponse{}, err
	}

	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Extension requested input"
	}
	if request.SubmitButtonText == "" {
		request.SubmitButtonText = "Submit"
	}

	fmt.Fprintln(b.Out)
	fmt.Fprintf(b.Out, "? %s\n", title)
	if helpText := strings.TrimSpace(request.HelpText); helpText != "" {
		fmt.Fprintln(b.Out, helpText)
	}
	if placeholder := strings.TrimSpace(request.Placeholder); placeholder != "" {
		fmt.Fprintf(b.Out, "Hint: %s\n", placeholder)
	}
	if request.DefaultValue != "" {
		fmt.Fprintf(b.Out, "Default: %s\n", request.DefaultValue)
	}
	fmt.Fprintf(b.Out, "%s> ", request.SubmitButtonText)

	line, err := b.reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		if err == io.EOF {
			return UIInputResponse{Status: UIInputStatusDismissed}, nil
		}
		return UIInputResponse{}, err
	}

	value := strings.TrimRight(line, "\r\n")
	if value == "" && request.DefaultValue != "" {
		value = request.DefaultValue
	}
	if request.Required && strings.TrimSpace(value) == "" {
		return UIInputResponse{Status: UIInputStatusDismissed}, nil
	}
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: value}, nil
}

func readerIsTerminal(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok || file == nil {
		return false
	}
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}
