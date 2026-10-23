package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestMetricsOutput(t *testing.T) {
	m := New()
	out := scrape(t, m)
	for _, want := range []string{
		`pg_backup_runs_total{result="failure"} 0`,
		`pg_backup_runs_total{result="success"} 0`,
		`pg_backup_last_success_timestamp_seconds 0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fresh metrics should contain %q", want)
		}
	}

	at := time.Unix(1_800_000_000, 0)
	m.BackupSucceeded(at, 42*time.Second, 1234, 5678)
	m.BackupFailed()
	m.Verified(at, 7*time.Second, true)
	out = scrape(t, m)
	for _, want := range []string{
		"pg_backup_last_success_timestamp_seconds 1.8e+09",
		"pg_backup_last_duration_seconds 42",
		"pg_backup_last_size_bytes 1234",
		"pg_backup_last_raw_size_bytes 5678",
		"pg_backup_last_verify_success 1",
		"pg_backup_last_verify_duration_seconds 7",
		`pg_backup_runs_total{result="success"} 1`,
		`pg_backup_runs_total{result="failure"} 1`,
		`pg_backup_verify_runs_total{result="success"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	m.Verified(at, time.Second, false)
	if out = scrape(t, m); !strings.Contains(out, "pg_backup_last_verify_success 0") {
		t.Error("failed drill should set the gauge to 0")
	}
}
