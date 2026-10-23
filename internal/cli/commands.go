package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"filippo.io/age"
	"github.com/spf13/cobra"

	"github.com/ramaraihan12/pg-backup/internal/backup"
	"github.com/ramaraihan12/pg-backup/internal/config"
	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/pg"
	"github.com/ramaraihan12/pg-backup/internal/retention"
	"github.com/ramaraihan12/pg-backup/internal/verify"
)

func (a *app) backupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Dump the database, compress, encrypt and upload it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			b := &backup.Backuper{Catalog: cat, Tools: a.tools(), Recipients: rec, DatabaseURL: dsn, Log: a.log}
			m, err := b.Run(cmd.Context())
			if err != nil {
				return err
			}
			printBackup(cmd.OutOrStdout(), m)
			return nil
		},
	}
}

func printBackup(w io.Writer, m *manifest.Manifest) {
	fmt.Fprintf(w, "backup %s  %s (postgres %s)\n", m.ID, m.Database, m.ServerVersion)
	fmt.Fprintf(w, "  raw        %s\n", human(m.RawBytes))
	ratio := ""
	if m.StoredBytes > 0 {
		ratio = fmt.Sprintf("  (%.1fx smaller, zstd + age)", float64(m.RawBytes)/float64(m.StoredBytes))
	}
	fmt.Fprintf(w, "  stored     %s%s\n", human(m.StoredBytes), ratio)
	fmt.Fprintf(w, "  rows       %s in %d tables\n", thousands(m.Rows()), len(m.Tables))
	fmt.Fprintf(w, "  took       %.1fs\n", m.DurationSeconds)
	if rss := peakRSS(); rss > 0 {
		fmt.Fprintf(w, "  peak rss   %s\n", human(rss))
	}
	fmt.Fprintf(w, "  checksum   %s\n", m.Checksum[:16]+"...")
	fmt.Fprintf(w, "  object     %s\n", m.Object)
}

func (a *app) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the backups of the configured database, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat, _, err := a.catalog()
			if err != nil {
				return err
			}
			l, err := cat.List(cmd.Context())
			if err != nil {
				return err
			}
			for _, p := range l.Problems {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", p)
			}
			if len(l.Backups) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no backups of %s under %s/\n", cat.Database, cat.Prefix)
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTAKEN (UTC)\tSTORED\tRAW\tROWS\tTOOK\tSERVER")
			for _, m := range l.Backups {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.1fs\t%s\n", m.ID, m.Time().Format("2006-01-02 15:04"),
					human(m.StoredBytes), human(m.RawBytes), thousands(m.Rows()), m.DurationSeconds, m.ServerVersion)
			}
			return tw.Flush()
		},
	}
}

func (a *app) identities() ([]age.Identity, error) { return a.cfg.Identities() }

func (a *app) restorer(cat backup.Catalog) (*backup.Restorer, error) {
	ids, err := a.identities()
	if err != nil {
		return nil, err
	}
	return &backup.Restorer{Catalog: cat, Tools: a.tools(), Identities: ids}, nil
}

func (a *app) restoreCmd() *cobra.Command {
	var to, id string
	var noOwner, noPrivileges bool
	cmd := &cobra.Command{
		Use:   "restore --to <database url>",
		Short: "Restore a backup into an existing, empty database",
		Long: "Downloads, decrypts and decompresses the backup straight into pg_restore.\n" +
			"The target database must already exist. It runs in one transaction, so a\n" +
			"failed restore leaves the database as it was.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := config.WithPassword(to, a.cfg.Secrets.DBPassword)
			if err != nil {
				return fmt.Errorf("--to: %w", err)
			}
			cat, _, err := a.catalog()
			if err != nil {
				return err
			}
			r, err := a.restorer(cat)
			if err != nil {
				return err
			}
			m, err := cat.Resolve(cmd.Context(), id)
			if err != nil {
				return err
			}
			start := time.Now()
			if err := r.Restore(cmd.Context(), m, target, pg.RestoreOptions{NoOwner: noOwner, NoPrivileges: noPrivileges}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restored %s (%s raw, %s rows) in %s\n",
				m.ID, human(m.RawBytes), thousands(m.Rows()), seconds(time.Since(start)))
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "database url to restore into (required)")
	cmd.Flags().StringVar(&id, "id", "latest", "backup id, or latest")
	cmd.Flags().BoolVar(&noOwner, "no-owner", false, "skip ownership commands (use when the roles do not exist on the target)")
	cmd.Flags().BoolVar(&noPrivileges, "no-privileges", false, "skip GRANT and REVOKE")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func (a *app) drill(cat backup.Catalog) (*verify.Drill, error) {
	r, err := a.restorer(cat)
	if err != nil {
		return nil, err
	}
	scratch, err := a.cfg.ScratchURL()
	if err != nil {
		return nil, err
	}
	return &verify.Drill{Restorer: r, ScratchURL: scratch, Checks: a.cfg.Verify.Checks}, nil
}

func (a *app) verifyCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Restore a backup into a scratch database and check it",
		Long: "The restore drill. Restores the backup into a fresh scratch database, compares\n" +
			"every table's row count with the manifest, runs the SQL checks from the config,\n" +
			"drops the scratch database and exits non-zero if anything failed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat, _, err := a.catalog()
			if err != nil {
				return err
			}
			d, err := a.drill(cat)
			if err != nil {
				return err
			}
			m, err := cat.Resolve(cmd.Context(), id)
			if err != nil {
				return err
			}
			rep := d.Run(cmd.Context(), m)
			printReport(cmd.OutOrStdout(), rep)
			if !rep.Passed {
				return errVerifyFailed
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "latest", "backup id, or latest")
	return cmd
}

func printReport(w io.Writer, r *verify.Report) {
	m := r.Backup
	fmt.Fprintf(w, "verify %s  %s, %s stored, %s rows expected\n", m.ID, m.Database, human(m.StoredBytes), thousands(m.Rows()))
	if r.Scratch != "" {
		fmt.Fprintf(w, "  scratch database  %s\n", r.Scratch)
	}
	restoreOK := r.RestoreTime > 0 && len(r.Tables) > 0
	fmt.Fprintf(w, "  restore           %s  %s\n", status(restoreOK), seconds(r.RestoreTime))
	if len(r.Tables) > 0 {
		fmt.Fprintf(w, "  row counts        %s  %s\n", status(allTablesOK(r)), seconds(r.CountTime))
		for _, t := range r.Tables {
			if t.OK {
				fmt.Fprintf(w, "    %-24s %12s  ok\n", t.Table, thousands(t.Actual))
			} else {
				fmt.Fprintf(w, "    %-24s %12s  FAIL (%s)\n", t.Table, thousands(t.Actual), t.Note)
			}
		}
	}
	for _, c := range r.Checks {
		if c.OK {
			fmt.Fprintf(w, "  check %-40s ok\n", strconv.Quote(c.Name))
		} else {
			fmt.Fprintf(w, "  check %-40s FAIL (%s)\n", strconv.Quote(c.Name), c.Detail)
		}
	}
	if r.Scratch != "" {
		fmt.Fprintf(w, "  cleanup           dropped  %s\n", seconds(r.CleanupTime))
	}
	if r.Passed {
		fmt.Fprintf(w, "PASS in %s\n", seconds(r.Total))
		return
	}
	fmt.Fprintf(w, "FAIL in %s\n", seconds(r.Total))
	for _, f := range r.Failures {
		fmt.Fprintf(w, "  - %s\n", f)
	}
}

func allTablesOK(r *verify.Report) bool {
	for _, t := range r.Tables {
		if !t.OK {
			return false
		}
	}
	return true
}

func status(ok bool) string {
	if ok {
		return "ok  "
	}
	return "FAIL"
}

func (a *app) policy() retention.Policy {
	r := a.cfg.Retention
	return retention.Policy{Daily: r.Daily, Weekly: r.Weekly, Monthly: r.Monthly}
}

func (a *app) pruneCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old backups under the retention policy (dry run unless --yes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat, _, err := a.catalog()
			if err != nil {
				return err
			}
			entries, err := cat.Prune(cmd.Context(), a.policy(), yes)
			if err != nil {
				return err
			}
			return printPrune(cmd.OutOrStdout(), a.policy(), entries, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "actually delete; without it prune only shows what it would do")
	return cmd
}

func printPrune(w io.Writer, p retention.Policy, entries []backup.PruneEntry, applied bool) error {
	if applied {
		fmt.Fprintf(w, "prune: keep %d daily, %d weekly, %d monthly\n", p.Daily, p.Weekly, p.Monthly)
	} else {
		fmt.Fprintf(w, "dry run, nothing is deleted (add --yes to delete): keep %d daily, %d weekly, %d monthly\n",
			p.Daily, p.Weekly, p.Monthly)
	}
	var kept, drop int
	var freed int64
	var failed []string
	for _, e := range entries {
		switch {
		case e.Keep:
			kept++
			fmt.Fprintf(w, "  keep    %s  %s\n", e.Backup.ID, strings.Join(e.Reasons, ", "))
		case e.Err != nil:
			drop++
			failed = append(failed, fmt.Sprintf("%s: %v", e.Backup.ID, e.Err))
			fmt.Fprintf(w, "  FAILED  %s  %v\n", e.Backup.ID, e.Err)
		case e.Deleted:
			drop++
			freed += e.Backup.StoredBytes
			fmt.Fprintf(w, "  deleted %s  %s\n", e.Backup.ID, human(e.Backup.StoredBytes))
		default:
			drop++
			freed += e.Backup.StoredBytes
			fmt.Fprintf(w, "  delete  %s  %s\n", e.Backup.ID, human(e.Backup.StoredBytes))
		}
	}
	verb := "would be deleted"
	if applied {
		verb = "deleted"
	}
	fmt.Fprintf(w, "%d kept, %d %s (%s)\n", kept, drop, verb, human(freed))
	if len(failed) > 0 {
		return fmt.Errorf("%d deletions failed: %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

func keygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Make an age key pair for encrypting backups",
		Long: "Prints a new age key pair. Put the public key in encryption.recipient (or\n" +
			"PGBACKUP_AGE_RECIPIENT) and keep the private key somewhere safe and separate\n" +
			"from the bucket: without it a backup cannot be restored.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := age.GenerateX25519Identity()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "# created: %s\n", time.Now().UTC().Format(time.RFC3339))
			fmt.Fprintf(w, "# public key: %s\n", id.Recipient())
			fmt.Fprintln(w, id)
			fmt.Fprintln(cmd.ErrOrStderr(), "keep the AGE-SECRET-KEY line private: set it as PGBACKUP_AGE_IDENTITY when you restore")
			return nil
		},
	}
}
