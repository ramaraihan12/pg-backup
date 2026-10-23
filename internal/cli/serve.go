package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ramaraihan12/pg-backup/internal/backup"
	"github.com/ramaraihan12/pg-backup/internal/metrics"
	"github.com/ramaraihan12/pg-backup/internal/notify"
	"github.com/ramaraihan12/pg-backup/internal/serve"
)

func (a *app) serveCmd() *cobra.Command {
	var runNow bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run backups on a schedule and expose /metrics",
		Long: "Runs backup, then the restore drill, on the cron schedule from the config.\n" +
			"Serves Prometheus metrics at /metrics and a health check at /healthz.\n" +
			"Sends a Telegram message when a run fails, if notify.telegram is on.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := serve.ValidateSchedule(a.cfg.Serve.Schedule); err != nil {
				return err
			}
			rec, err := a.cfg.Recipients()
			if err != nil {
				return err
			}
			dsn, err := a.cfg.DatabaseURL()
			if err != nil {
				return err
			}
			cat, st, err := a.catalog()
			if err != nil {
				return err
			}
			if err := st.EnsureBucket(cmd.Context()); err != nil {
				return err
			}

			job := &serve.Job{
				Backup:   &backup.Backuper{Catalog: cat, Tools: a.tools(), Recipients: rec, DatabaseURL: dsn, Log: a.log},
				Metrics:  metrics.New(),
				Notifier: notify.Nop{},
				Log:      a.log,
			}
			if a.cfg.Serve.Verify {
				d, err := a.drill(cat)
				if err != nil {
					return err
				}
				job.Verify = d
			}
			if a.cfg.Serve.Prune {
				p := a.policy()
				job.Prune = func(ctx context.Context) (int, error) {
					entries, err := cat.Prune(ctx, p, true)
					n := 0
					for _, e := range entries {
						if e.Deleted {
							n++
						}
					}
					return n, err
				}
			}
			if a.cfg.Notify.Telegram {
				job.Notifier = &notify.Telegram{Token: a.cfg.Secrets.TelegramToken, ChatID: a.cfg.Secrets.TelegramChatID}
			}

			return serve.Serve(cmd.Context(), serve.Options{
				Schedule: a.cfg.Serve.Schedule,
				Listen:   a.cfg.Serve.Listen,
				RunNow:   runNow,
			}, job)
		},
	}
	cmd.Flags().BoolVar(&runNow, "run-now", false, "also run once right after starting")
	return cmd
}
