package extensions

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/muesli/cancelreader"
	"github.com/pkg/errors"
	"golang.org/x/term"
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
	Message          string `json:"message,omitempty"`
	Placeholder      string `json:"placeholder,omitempty"`
	DefaultValue     string `json:"defaultValue,omitempty"`
	SubmitButtonText string `json:"submitButtonText,omitempty"`
	CancelButtonText string `json:"cancelButtonText,omitempty"`
	Required         bool   `json:"required,omitempty"`
	Secret           bool   `json:"secret,omitempty"`
}

// UIConfirmRequest describes a yes/no prompt requested by an extension.
type UIConfirmRequest struct {
	ID                string `json:"id,omitempty"`
	Title             string `json:"title"`
	Message           string `json:"message,omitempty"`
	ConfirmButtonText string `json:"confirmButtonText,omitempty"`
	CancelButtonText  string `json:"cancelButtonText,omitempty"`
}

// UISelectRequest describes a single-choice prompt requested by an extension.
type UISelectRequest struct {
	ID               string   `json:"id,omitempty"`
	Title            string   `json:"title"`
	Message          string   `json:"message,omitempty"`
	Options          []string `json:"options"`
	SubmitButtonText string   `json:"submitButtonText,omitempty"`
	CancelButtonText string   `json:"cancelButtonText,omitempty"`
}

// UINotifyRequest describes a fire-and-forget notification requested by an extension.
type UINotifyRequest struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

// UIInputResponse is returned to the extension after a UI input prompt resolves.
type UIInputResponse struct {
	Status    string `json:"status"`
	Value     string `json:"value,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// UIInputBroker routes extension UI input requests to the active user interface.
type UIInputBroker interface {
	Input(ctx context.Context, request UIInputRequest) (UIInputResponse, error)
}

// UIConfirmBroker routes extension confirmation requests to the active user interface.
type UIConfirmBroker interface {
	Confirm(ctx context.Context, request UIConfirmRequest) (UIInputResponse, error)
}

// UISelectBroker routes extension selection requests to the active user interface.
type UISelectBroker interface {
	Select(ctx context.Context, request UISelectRequest) (UIInputResponse, error)
}

// UINotifyBroker routes extension notifications to the active user interface.
type UINotifyBroker interface {
	Notify(ctx context.Context, request UINotifyRequest) (UIInputResponse, error)
}

type (
	uiInputBrokerKey    struct{}
	uiExtensionOwnerKey struct{}
)

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

// ContextWithUIExtensionOwner attaches the originating extension process generation to interactive UI requests.
func ContextWithUIExtensionOwner(ctx context.Context, owner UIExtensionOwner) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return ctx
	}
	return context.WithValue(ctx, uiExtensionOwnerKey{}, owner)
}

// UIExtensionOwnerFromContext returns the extension process generation that originated an interactive UI request.
func UIExtensionOwnerFromContext(ctx context.Context) (UIExtensionOwner, bool) {
	if ctx == nil {
		return UIExtensionOwner{}, false
	}
	owner, ok := ctx.Value(uiExtensionOwnerKey{}).(UIExtensionOwner)
	return owner, ok && strings.TrimSpace(owner.ExtensionID) != "" && owner.Generation > 0
}

// UIConfirmBrokerFromContext returns the run-scoped confirmation broker, if one exists.
func UIConfirmBrokerFromContext(ctx context.Context) (UIConfirmBroker, bool) {
	if ctx == nil {
		return nil, false
	}
	broker, ok := ctx.Value(uiInputBrokerKey{}).(UIConfirmBroker)
	return broker, ok && broker != nil
}

// UISelectBrokerFromContext returns the run-scoped selection broker, if one exists.
func UISelectBrokerFromContext(ctx context.Context) (UISelectBroker, bool) {
	if ctx == nil {
		return nil, false
	}
	broker, ok := ctx.Value(uiInputBrokerKey{}).(UISelectBroker)
	return broker, ok && broker != nil
}

// UINotifyBrokerFromContext returns the run-scoped notification broker, if one exists.
func UINotifyBrokerFromContext(ctx context.Context) (UINotifyBroker, bool) {
	if ctx == nil {
		return nil, false
	}
	broker, ok := ctx.Value(uiInputBrokerKey{}).(UINotifyBroker)
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
	if message := strings.TrimSpace(request.Message); message != "" {
		fmt.Fprintln(b.Out, message)
	}
	if placeholder := strings.TrimSpace(request.Placeholder); placeholder != "" {
		fmt.Fprintf(b.Out, "Hint: %s\n", placeholder)
	}
	if request.DefaultValue != "" {
		fmt.Fprintf(b.Out, "Default: %s\n", request.DefaultValue)
	}
	fmt.Fprintf(b.Out, "%s> ", request.SubmitButtonText)

	line, err := b.readLine(ctx)
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

// Confirm asks the user for a yes/no decision.
func (b *TerminalUIInputBroker) Confirm(ctx context.Context, request UIConfirmRequest) (UIInputResponse, error) {
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
		title = "Extension requested confirmation"
	}
	confirmText := strings.TrimSpace(request.ConfirmButtonText)
	if confirmText == "" {
		confirmText = "Confirm"
	}
	cancelText := strings.TrimSpace(request.CancelButtonText)
	if cancelText == "" {
		cancelText = "Cancel"
	}

	fmt.Fprintln(b.Out)
	fmt.Fprintf(b.Out, "? %s\n", title)
	if message := strings.TrimSpace(request.Message); message != "" {
		fmt.Fprintln(b.Out, message)
	}
	fmt.Fprintf(b.Out, "%s/%s> ", confirmText, cancelText)

	line, err := b.readLine(ctx)
	if err != nil && len(line) == 0 {
		if err == io.EOF {
			return UIInputResponse{Status: UIInputStatusDismissed}, nil
		}
		return UIInputResponse{}, err
	}

	answer := strings.ToLower(strings.TrimSpace(line))
	confirmed := answer == "y" || answer == "yes" || answer == "1" || answer == strings.ToLower(confirmText)
	if confirmed {
		return UIInputResponse{Status: UIInputStatusSubmitted, Confirmed: true, Value: "true"}, nil
	}
	return UIInputResponse{Status: UIInputStatusDismissed, Confirmed: false, Value: "false"}, nil
}

// Select asks the user to choose one option.
func (b *TerminalUIInputBroker) Select(ctx context.Context, request UISelectRequest) (UIInputResponse, error) {
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
		title = "Extension requested selection"
	}
	submitText := strings.TrimSpace(request.SubmitButtonText)
	if submitText == "" {
		submitText = "Select"
	}

	fmt.Fprintln(b.Out)
	fmt.Fprintf(b.Out, "? %s\n", title)
	if message := strings.TrimSpace(request.Message); message != "" {
		fmt.Fprintln(b.Out, message)
	}
	for i, option := range request.Options {
		fmt.Fprintf(b.Out, "%d. %s\n", i+1, option)
	}
	fmt.Fprintf(b.Out, "%s> ", submitText)

	line, err := b.readLine(ctx)
	if err != nil && len(line) == 0 {
		if err == io.EOF {
			return UIInputResponse{Status: UIInputStatusDismissed}, nil
		}
		return UIInputResponse{}, err
	}

	answer := strings.TrimSpace(line)
	for i, option := range request.Options {
		if answer == fmt.Sprintf("%d", i+1) || strings.EqualFold(answer, option) {
			return UIInputResponse{Status: UIInputStatusSubmitted, Value: option}, nil
		}
	}
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: answer}, nil
}

// Notify displays a one-way notification.
func (b *TerminalUIInputBroker) Notify(ctx context.Context, request UINotifyRequest) (UIInputResponse, error) {
	if b == nil || !b.Interactive {
		return UIInputResponse{Status: UIInputStatusUnavailable, Reason: "terminal input is not available"}, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return UIInputResponse{}, err
	}

	title := strings.TrimSpace(request.Title)
	message := strings.TrimSpace(request.Message)
	if title != "" {
		fmt.Fprintf(b.Out, "\n! %s\n", title)
	}
	if message != "" {
		fmt.Fprintf(b.Out, "%s\n", message)
	}
	return UIInputResponse{Status: UIInputStatusSubmitted}, nil
}

// readLine interrupts a terminal read without closing the caller's stdin. The
// broker mutex serializes access; preserve read-ahead between ordinary prompts.
func (b *TerminalUIInputBroker) readLine(ctx context.Context) (string, error) {
	if _, ok := b.In.(cancelreader.File); !ok {
		return b.reader.ReadString('\n')
	}
	reader, err := cancelreader.NewReader(b.In)
	if err != nil {
		return "", errors.Wrap(err, "failed to prepare cancellable terminal input")
	}
	defer func() { _ = reader.Close() }()
	buffered, _ := b.reader.Peek(b.reader.Buffered())
	unread := append([]byte(nil), buffered...)
	b.reader.Reset(io.MultiReader(bytes.NewReader(unread), reader))
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		reader.Cancel()
		close(cancelled)
	})
	line, err := b.reader.ReadString('\n')
	if !stop() {
		<-cancelled // Do not close cancellation descriptors while the callback uses them.
	}
	if ctx.Err() != nil {
		b.reader.Reset(b.In)
		return "", ctx.Err()
	}
	return line, err
}

func readerIsTerminal(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok || file == nil {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
