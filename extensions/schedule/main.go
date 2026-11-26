// kodelet-extension-schedule runs agent instructions on recurring schedules.
// Without arguments it serves Kodelet's extension protocol with tools to manage
// the schedules of the current workspace; the daemon subcommand runs them.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCommand().ExecuteContext(ctx)
	cancel()
	if err != nil {
		os.Exit(1)
	}
}
