// Package verify runs the restore drill: restore a backup into a scratch
// database, compare it with the manifest, run the user's SQL checks, and drop
// the scratch database again.
package verify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ramaraihan12/pg-backup/internal/backup"
	"github.com/ramaraihan12/pg-backup/internal/config"
	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/pg"
)

// Drill restores backups into throwaway databases.
type Drill struct {
	Restorer *backup.Restorer
	// ScratchURL points at the server where scratch databases are created.
	ScratchURL string
	Checks     []config.Check
}

// TableResult compares one table with the manifest.
type TableResult struct {
	Table    string
	Expected int64
	Actual   int64
	OK       bool
	Note     string
}

// CheckResult is the outcome of one user-defined SQL check.
type CheckResult struct {
	Name   string
	OK     bool
	Detail string
}

// Report is the outcome of one drill.
type Report struct {
	Backup   *manifest.Manifest
	Scratch  string
	Passed   bool
	Failures []string

	Tables []TableResult
	Checks []CheckResult

	RestoreTime time.Duration
	CountTime   time.Duration
	ChecksTime  time.Duration
	CleanupTime time.Duration
	Total       time.Duration
}

// Run restores backup m into a new scratch database and checks it. Problems
// with the backup are reported in the Report, not as an error, so a caller
// can always print or export the result. The scratch database is dropped even
// when ctx is cancelled.
func (d *Drill) Run(ctx context.Context, m *manifest.Manifest) *Report {
	start := time.Now()
	r := &Report{Backup: m}
	defer func() {
		r.Total = time.Since(start)
		r.Passed = len(r.Failures) == 0
	}()
	fail := func(format string, args ...any) { r.Failures = append(r.Failures, fmt.Sprintf(format, args...)) }

	r.Scratch = scratchName(m.ID)
	if err := pg.CreateDatabase(ctx, d.ScratchURL, r.Scratch); err != nil {
		fail("create scratch database: %v", err)
		return r
	}
	defer func() {
		t := time.Now()
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := pg.DropDatabase(cctx, d.ScratchURL, r.Scratch); err != nil {
			fail("drop scratch database %s (drop it by hand): %v", r.Scratch, err)
		}
		r.CleanupTime = time.Since(t)
	}()

	scratchDSN, err := pg.WithDatabase(d.ScratchURL, r.Scratch)
	if err != nil {
		fail("%v", err)
		return r
	}

	t := time.Now()
	err = d.Restorer.Restore(ctx, m, scratchDSN, pg.RestoreOptions{NoOwner: true, NoPrivileges: true})
	r.RestoreTime = time.Since(t)
	if err != nil {
		fail("restore: %v", err)
		return r
	}

	t = time.Now()
	actual, err := pg.CountRestored(ctx, scratchDSN)
	r.CountTime = time.Since(t)
	if err != nil {
		fail("count rows in the restored database: %v", err)
		return r
	}
	r.Tables = compare(m.Tables, actual)
	for _, tr := range r.Tables {
		if !tr.OK {
			fail("table %s: %s", tr.Table, tr.Note)
		}
	}

	t = time.Now()
	for _, c := range d.Checks {
		res := CheckResult{Name: c.Name}
		ok, err := pg.RunCheck(ctx, scratchDSN, c.SQL)
		switch {
		case err != nil:
			res.Detail = err.Error()
		case !ok:
			res.Detail = "returned false"
		default:
			res.OK = true
		}
		if !res.OK {
			fail("check %q: %s", c.Name, res.Detail)
		}
		r.Checks = append(r.Checks, res)
	}
	r.ChecksTime = time.Since(t)
	return r
}

// compare matches restored counts against the manifest, table by table. A
// table that exists on only one side is a failure.
func compare(want, got []manifest.Table) []TableResult {
	actual := map[string]int64{}
	for _, t := range got {
		actual[t.Qualified()] = t.Rows
	}
	var out []TableResult
	seen := map[string]bool{}
	for _, w := range want {
		name := w.Qualified()
		seen[name] = true
		g, ok := actual[name]
		tr := TableResult{Table: name, Expected: w.Rows, Actual: g}
		switch {
		case !ok:
			tr.Note = "missing after restore"
		case g != w.Rows:
			tr.Note = fmt.Sprintf("expected %d rows, restored %d", w.Rows, g)
		default:
			tr.OK = true
		}
		out = append(out, tr)
	}
	for _, g := range got {
		if !seen[g.Qualified()] {
			out = append(out, TableResult{Table: g.Qualified(), Expected: -1, Actual: g.Rows,
				Note: "not in the manifest"})
		}
	}
	return out
}

func scratchName(id string) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "pgbackup_verify_" + strings.ToLower(id) + "_" + hex.EncodeToString(b[:])
}
