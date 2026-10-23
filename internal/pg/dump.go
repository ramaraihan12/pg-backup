package pg

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/ramaraihan12/pg-backup/internal/manifest"

	"github.com/jackc/pgx/v5"
)

// DumpInfo is what we learned while dumping.
type DumpInfo struct {
	ServerVersion string
	PgDumpVersion string
	// Tables holds exact row counts from the same snapshot pg_dump read.
	Tables []manifest.Table
}

// Dump streams a custom-format pg_dump of the database in dsn to w.
//
// It opens one repeatable-read transaction and exports its snapshot. pg_dump
// is started with --snapshot, and the row counts are taken inside that same
// transaction while the dump runs. So the counts describe exactly the data
// that is in the dump, even if writes continue during the backup.
//
// pg_dump's own compression is turned off: the pipeline compresses the whole
// stream with zstd, and compressing twice only costs CPU.
func (t Tools) Dump(ctx context.Context, dsn string, w io.Writer) (*DumpInfo, error) {
	pgDump, err := t.bin("pg_dump")
	if err != nil {
		return nil, err
	}
	dumpVersion, err := t.Version(ctx, "pg_dump")
	if err != nil {
		return nil, err
	}
	env, err := connEnv(dsn)
	if err != nil {
		return nil, err
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to source: %w", err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin snapshot transaction: %w", err)
	}
	defer tx.Rollback(context.Background())

	info := &DumpInfo{PgDumpVersion: dumpVersion}
	var snapshot string
	if err := tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&snapshot); err != nil {
		return nil, fmt.Errorf("export snapshot: %w", err)
	}
	if err := tx.QueryRow(ctx, "SHOW server_version").Scan(&info.ServerVersion); err != nil {
		return nil, fmt.Errorf("read server version: %w", err)
	}
	info.ServerVersion = shortVersion(info.ServerVersion)

	// Count rows while pg_dump runs. The transaction is used by this one
	// goroutine only, until we receive from done.
	type counted struct {
		tables []manifest.Table
		err    error
	}
	done := make(chan counted, 1)
	countCtx, cancelCount := context.WithCancel(ctx)
	defer cancelCount()
	go func() {
		tables, err := CountTables(countCtx, tx)
		done <- counted{tables, err}
	}()

	cmd := exec.CommandContext(ctx, pgDump,
		"--format=custom", "--compress=0", "--snapshot="+snapshot)
	cmd.Env = env
	cmd.Stdout = w
	stderr := &tail{n: 4096}
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if runErr != nil {
		cancelCount()
		<-done
		return nil, cmdError("pg_dump", runErr, stderr)
	}

	c := <-done
	if c.err != nil {
		return nil, c.err
	}
	info.Tables = c.tables
	return info, nil
}

// shortVersion drops the distro suffix: "17.11 (Debian 17.11-1.pgdg12+2)" becomes "17.11".
func shortVersion(v string) string {
	if f := strings.Fields(v); len(f) > 0 {
		return f[0]
	}
	return v
}
