package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/pkg/errors"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	binaryName := flag.String("binary", "", "binary to resolve (ripgrep or fd)")
	goos := flag.String("goos", "linux", "target operating system")
	goarch := flag.String("goarch", "amd64", "target architecture")
	flag.Parse()

	if *binaryName == "" {
		return errors.New("binary is required")
	}

	spec, err := resolveSpec(*binaryName)
	if err != nil {
		return err
	}

	metadata, err := binaries.ResolveDownloadMetadata(context.Background(), spec, *goos, *goarch)
	if err != nil {
		return err
	}

	fmt.Println(metadata.URL)
	fmt.Println(metadata.Checksum)
	return nil
}

func resolveSpec(binaryName string) (binaries.BinarySpec, error) {
	switch binaryName {
	case "ripgrep":
		return binaries.RipgrepSpec(), nil
	case "fd":
		return binaries.FdSpec(), nil
	default:
		return binaries.BinarySpec{}, errors.Errorf("unsupported binary: %s", binaryName)
	}
}
