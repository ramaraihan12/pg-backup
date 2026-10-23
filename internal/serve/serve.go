// Package serve runs backups on a cron schedule and exposes /metrics.
package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/metrics"
	"github.com/ramaraihan12/pg-backup/internal/notify"
	"github.com/ramaraihan12/pg-backup/internal/verify"
)

// Backuper takes a backup.
type Backuper interface {
	Run(ctx context.Context) (*manifest.Manifest, error)
}

// Verifier runs the restore drill on a backup.
type Verifier interface {
	Run(ctx context.Context, m *manifest.Manifest) *verify.Report
}

// Job is one scheduled run: backup, then verify, then optionally prune.
type Job struct {
	Backup   Backuper
	Verify   Verifier                               // nil skips the drill
	Prune    func(ctx context.Context) (int, error) // nil skips pruning
	Metrics  *metrics.Metrics
	Notifier notify.Notifier
	Log      *slog.Logger
	Now      func() time.Time
}

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// Run executes the job once. The returned error is also sent to the notifier.
func (j *Job) Run(ctx context.Context) error {
	m, err := j.Backup.Run(ctx)
	if err != nil {
		j.Metrics.BackupFailed()
		return j.fail(ctx, fmt.Sprintf("pg-backup: backup failed: %v", err), err)
	}
	j.Metrics.BackupSucceeded(j.now(), time.Duration(m.DurationSeconds*float64(time.Second)), m.StoredBytes, m.RawBytes)
	j.Log.Info("backup done", "id", m.ID, "stored_bytes", m.StoredBytes, "tables", len(m.Tables), "rows", m.Rows())

	if j.Verify != nil {
		rep := j.Verify.Run(ctx, m)
		j.Metrics.Verified(j.now(), rep.Total, rep.Passed)
		if !rep.Passed {
			msg := fmt.Sprintf("pg-backup: restore drill FAILED for %s (%s)\n- %s",
				m.Database, m.ID, strings.Join(rep.Failures, "\n- "))
			return j.fail(ctx, msg, errors.New("restore drill failed: "+strings.Join(rep.Failures, "; ")))
		}
		j.Log.Info("restore drill passed", "id", m.ID, "seconds", rep.Total.Seconds())
	}

	if j.Prune != nil {
		n, err := j.Prune(ctx)
		if err != nil {
			return j.fail(ctx, fmt.Sprintf("pg-backup: prune failed: %v", err), err)
		}
		j.Log.Info("pruned old backups", "deleted", n)
	}
	return nil
}

func (j *Job) fail(ctx context.Context, message string, err error) error {
	j.Log.Error("run failed", "error", err)
	// Do not let a cancelled run swallow the alert.
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if nerr := j.Notifier.Notify(nctx, message); nerr != nil {
		j.Log.Error("could not send notification", "error", nerr)
	}
	return err
}

// Options configures Serve.
type Options struct {
	Schedule string
	Listen   string
	RunNow   bool
}

// ValidateSchedule checks a standard five-field cron expression.
func ValidateSchedule(spec string) error {
	if _, err := cron.ParseStandard(spec); err != nil {
		return fmt.Errorf("bad schedule %q: %w", spec, err)
	}
	return nil
}

// Serve blocks until ctx is cancelled. A run that is still going when the
// next tick arrives is skipped, not stacked.
func Serve(ctx context.Context, o Options, job *Job) error {
	if err := ValidateSchedule(o.Schedule); err != nil {
		return err
	}
	log := job.Log
	cl := cronLogger{log}
	c := cron.New(cron.WithChain(cron.Recover(cl), cron.SkipIfStillRunning(cl)))
	if _, err := c.AddFunc(o.Schedule, func() { _ = job.Run(ctx) }); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", job.Metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	srv := &http.Server{Addr: o.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	c.Start()
	log.Info("serving", "schedule", o.Schedule, "listen", o.Listen)
	if o.RunNow {
		go func() { _ = job.Run(ctx) }()
	}

	select {
	case err := <-errc:
		c.Stop()
		return fmt.Errorf("metrics server: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	<-c.Stop().Done() // a running job sees the cancelled ctx and stops
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

type cronLogger struct{ log *slog.Logger }

func (l cronLogger) Info(msg string, kv ...any) { l.log.Info(msg, kv...) }
func (l cronLogger) Error(err error, msg string, kv ...any) {
	l.log.Error(msg, append(kv, "error", err)...)
}
