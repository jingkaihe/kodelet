// kodelet-clipboard reads the local desktop clipboard once for the TUI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/pkg/errors"
)

var version = "dev"

const readTimeout = 10 * time.Second

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 1 {
		switch args[0] {
		case "--version":
			_, err := fmt.Fprintln(out, "kodelet-clipboard", version)
			return err
		case "read-v1":
			return readOnce(out)
		}
	}
	return errors.New("usage: kodelet-clipboard read-v1 | --version")
}

func readOnce(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var files fileReader
		if runtime.GOOS == "darwin" {
			files = readFinderFiles
		}
		result := readSelection(ctx, files, readDesktopClipboard)
		done <- result.writeTo(out)
	}()
	// Native reads can ignore cancellation. Return to main and exit the process
	// without joining the worker, even if the TUI is no longer around to kill us.
	select {
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "reading clipboard")
	case err := <-done:
		return err
	}
}

type response struct {
	kind byte
	data []byte
}

func (r response) writeTo(out io.Writer) error {
	if _, err := out.Write([]byte{r.kind}); err != nil {
		return err
	}
	_, err := out.Write(r.data)
	return err
}
