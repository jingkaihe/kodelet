// kodelet-clipboard reads the local desktop clipboard once for the TUI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/pkg/errors"
)

var version = "dev"

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
			var files fileReader
			if runtime.GOOS == "darwin" {
				files = readFinderFiles
			}
			result := readSelection(context.Background(), files, readDesktopClipboard)
			return result.writeTo(out)
		}
	}
	return errors.New("usage: kodelet-clipboard read-v1 | --version")
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
