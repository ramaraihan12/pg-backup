package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ramaraihan12/pg-backup/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, version, os.Args[1:])
	stop()
	os.Exit(code)
}
