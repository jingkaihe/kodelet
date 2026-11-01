package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

// Serve speaks the extension protocol over in and out using Content-Length
// framing. It returns nil when in reaches EOF, after running session.end
// handlers with a bounded timeout, or the context error when ctx ends.
func (e *Extension) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	writer := jsonrpc.NewFrameWriter(out)
	runtime := NewRuntime(e, TransportFunc(func(_ context.Context, message []byte) error {
		return writer.Write(message)
	}), nil)
	reader := jsonrpc.NewFrameReader(in)
	readErr := make(chan error, 1)
	go func() {
		for {
			payload, err := reader.Read()
			if err != nil {
				readErr <- err
				return
			}
			if err := runtime.Receive(payload); err != nil {
				readErr <- err
				return
			}
		}
	}()
	select {
	case err := <-readErr:
		runtime.Close(errHostDisconnected)
		if errors.Is(err, io.EOF) || errors.Is(err, ErrRuntimeClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		runtime.Close(context.Cause(ctx))
		return ctx.Err()
	}
}

// Run serves the extension over the process's stdin and stdout. While it runs,
// os.Stdout is redirected to stderr so stray prints cannot corrupt the
// protocol stream.
func (e *Extension) Run(ctx context.Context) error {
	protocol := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = protocol }()
	return e.Serve(ctx, os.Stdin, protocol)
}

// Main runs the extension as an installed executable and exits the process.
// SIGTERM triggers a graceful shutdown that runs session.end handlers.
func (e *Extension) Main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	err := e.Run(ctx)
	stop()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "extension: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
