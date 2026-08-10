//go:build linux

package main

import (
	"os"
	"strings"
	"unsafe"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func protectRemoteACPProcessSecrets(flagNames ...string) error {
	scrubProcessFlagValues(os.Args, flagNames...)
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return errors.Wrap(err, "failed to protect remote ACP credentials from child processes")
	}
	return nil
}

func scrubProcessFlagValues(args []string, flagNames ...string) {
	flags := make(map[string]struct{}, len(flagNames))
	for _, name := range flagNames {
		if name = strings.TrimSpace(name); name != "" {
			flags["--"+name] = struct{}{}
		}
	}
	for index, arg := range args {
		name, _, hasValue := strings.Cut(arg, "=")
		if _, ok := flags[name]; !ok {
			continue
		}
		if hasValue {
			scrubArgumentBytes(arg, len(name)+1)
			continue
		}
		if index+1 < len(args) {
			scrubArgumentBytes(args[index+1], 0)
		}
	}
}

func scrubArgumentBytes(argument string, start int) {
	if start < 0 || start >= len(argument) {
		return
	}
	bytes := unsafe.Slice(unsafe.StringData(argument), len(argument))
	clear(bytes[start:])
}
