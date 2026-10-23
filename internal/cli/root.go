// Package cli is the command line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/ramaraihan12/pg-backup/internal/backup"
	"github.com/ramaraihan12/pg-backup/internal/config"
	"github.com/ramaraihan12/pg-backup/internal/pg"
	"github.com/ramaraihan12/pg-backup/internal/store"
)

const defaultConfigFile = "pg-backup.yaml"

// app is what the commands share: the loaded config and a logger.
type app struct {
	cfgPath string
	verbose bool
	cfg     *config.Config
	log     *slog.Logger
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, version string, args []string) int {
	root := newRoot(version)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRoot(version string) *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "pg-backup",
		Short:         "Stream Postgres backups to S3, encrypted, and prove them by restoring",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			switch cmd.Name() {
			case "keygen", "help", "version", "completion":
				return nil
			}
			return a.load(cmd.Name() == "serve")
		},
	}
	root.PersistentFlags().StringVarP(&a.cfgPath, "config", "c", "",
		"config file (default: $PGBACKUP_CONFIG, then ./"+defaultConfigFile+" if it exists)")
	root.PersistentFlags().BoolVarP(&a.verbose, "verbose", "v", false, "log progress to stderr")

	root.AddCommand(
		a.backupCmd(),
		a.listCmd(),
		a.restoreCmd(),
		a.verifyCmd(),
		a.pruneCmd(),
		a.serveCmd(),
		keygenCmd(),
	)
	return root
}

func (a *app) load(isServe bool) error {
	path := a.cfgPath
	if path == "" {
		path = os.Getenv("PGBACKUP_CONFIG")
	}
	if path == "" {
		if _, err := os.Stat(defaultConfigFile); err == nil {
			path = defaultConfigFile
		}
	}
	cfg, err := config.Load(path, os.Getenv)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.cfg = cfg
	level := slog.LevelWarn
	if a.verbose || isServe {
		level = slog.LevelInfo
	}
	a.log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	return nil
}

func (a *app) s3() (*store.S3, error) {
	s := a.cfg.Storage
	return store.NewS3(store.S3Options{
		Endpoint: s.Endpoint, Bucket: s.Bucket, Region: s.Region, UseSSL: s.UseSSL,
		AccessKey: a.cfg.Secrets.S3AccessKey, SecretKey: a.cfg.Secrets.S3SecretKey,
	})
}

func (a *app) catalog() (backup.Catalog, *store.S3, error) {
	st, err := a.s3()
	if err != nil {
		return backup.Catalog{}, nil, err
	}
	name, err := a.cfg.DatabaseName()
	if err != nil {
		return backup.Catalog{}, nil, err
	}
	return backup.Catalog{Store: st, Prefix: a.cfg.Storage.Prefix, Database: name}, st, nil
}

func (a *app) tools() pg.Tools { return pg.Tools{BinDir: a.cfg.Postgres.BinDir} }

var errVerifyFailed = errors.New("restore drill failed")
