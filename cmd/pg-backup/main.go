package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/ramaraihan12/pg-backup/internal/cli"
)

var version = "dev"

func main() {
	// The streaming path holds a few fixed buffers (one 16 MiB upload part, the
	// zstd window) and little else. A low GC target keeps garbage from piling
	// up on top of them, which keeps resident memory flat for any dump size.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(25)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, version, os.Args[1:])
	stop()
	os.Exit(code)
}
