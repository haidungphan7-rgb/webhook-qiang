package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	urfavecli "github.com/urfave/cli/v3"

	appcli "github.com/yuandzhang/webhook-zq/internal/cli"
)

// main CLI application entrypoint.
func main() {
	if err := run(); err != nil {
		// ExitCoder is the published contract between subcommands and
		// scripts: status (0/3/4), doctor (0/1), tray --exit (0/1).
		// urfave/cli already prints and exits for most of these inside
		// Run; this is the backstop for every path it does not cover,
		// and it passes the code through instead of flattening it.
		var exitErr urfavecli.ExitCoder
		if errors.As(err, &exitErr) {
			if msg := exitErr.Error(); msg != "" {
				_, _ = fmt.Fprintln(os.Stderr, msg)
			}

			os.Exit(exitErr.ExitCode())
		}

		_, _ = fmt.Fprintln(os.Stderr, err.Error())

		os.Exit(1)
	}
}

// run is the entry point of the program. The code is in separate function to allow executing deferred functions
// before exiting (os.Exit does not execute deferred functions).
func run() error {
	defer runtime.Gosched() // increase the chance of running deferred functions before exiting

	var ctx, cancel = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return appcli.NewApp().Run(ctx, os.Args)
}
