package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramaraihan12/pg-backup/internal/backup"
	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/retention"
	"github.com/ramaraihan12/pg-backup/internal/verify"
)

func entry(id string, keep bool, reasons ...string) backup.PruneEntry {
	return backup.PruneEntry{
		Backup:  &manifest.Manifest{ID: id, StoredBytes: 2 << 20},
		Keep:    keep,
		Reasons: reasons,
	}
}

func TestPrintPruneDryRun(t *testing.T) {
	var out bytes.Buffer
	err := printPrune(&out, retention.Policy{Daily: 7, Weekly: 4, Monthly: 6}, []backup.PruneEntry{
		entry("20261023T030000Z", true, "newest", "daily 2026-10-23"),
		entry("20261001T030000Z", false),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"dry run, nothing is deleted (add --yes to delete)",
		"keep    20261023T030000Z  newest, daily 2026-10-23",
		"delete  20261001T030000Z  2.0 MiB",
		"1 kept, 1 would be deleted (2.0 MiB)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestPrintPruneApplied(t *testing.T) {
	var out bytes.Buffer
	deleted := entry("20261001T030000Z", false)
	deleted.Deleted = true
	failed := entry("20260901T030000Z", false)
	failed.Err = errors.New("access denied")

	err := printPrune(&out, retention.Policy{Daily: 1}, []backup.PruneEntry{entry("20261023T030000Z", true, "newest"), deleted, failed}, true)
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("a failed delete must give a non-zero exit, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"deleted 20261001T030000Z", "FAILED  20260901T030000Z", "1 kept, 2 deleted"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "dry run") {
		t.Error("an applied prune should not say dry run")
	}
}

func report(passed bool, failures ...string) *verify.Report {
	return &verify.Report{
		Backup:  &manifest.Manifest{ID: "20261023T030000Z", Database: "pgbackup_demo", StoredBytes: 5 << 20, Tables: []manifest.Table{{Schema: "public", Name: "orders", Rows: 10}}},
		Scratch: "pgbackup_verify_x", Passed: passed, Failures: failures,
		Restored: true, RestoreTime: 2 * time.Second, Total: 3 * time.Second,
		Tables: []verify.TableResult{{Table: "public.orders", Expected: 10, Actual: 10, OK: true}},
		Checks: []verify.CheckResult{{Name: "orders have customers", OK: true}},
	}
}

func TestPrintReportPass(t *testing.T) {
	var out bytes.Buffer
	printReport(&out, report(true))
	got := out.String()
	for _, want := range []string{"PASS in 3.0s", "public.orders", `"orders have customers"`, "dropped"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FAIL") {
		t.Errorf("unexpected FAIL in:\n%s", got)
	}
}

func TestPrintReportFail(t *testing.T) {
	r := report(false, "restore: checksum mismatch")
	r.Tables = nil
	var out bytes.Buffer
	printReport(&out, r)
	got := out.String()
	for _, want := range []string{"FAIL in 3.0s", "  - restore: checksum mismatch"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestPrintReportRestoredWithNoTables(t *testing.T) {
	r := report(true)
	r.Tables = nil
	r.Restored = true
	var out bytes.Buffer
	printReport(&out, r)
	if strings.Contains(out.String(), "restore           FAIL") {
		t.Errorf("a restore that worked must not print FAIL:\n%s", out.String())
	}
}
