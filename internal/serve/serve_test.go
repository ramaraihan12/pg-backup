package serve

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/metrics"
	"github.com/ramaraihan12/pg-backup/internal/verify"
)

type fakeBackup struct {
	err   error
	calls int
}

func (f *fakeBackup) Run(ctx context.Context) (*manifest.Manifest, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &manifest.Manifest{ID: "20261023T031500Z", Database: "toko_maju", DurationSeconds: 3, StoredBytes: 100, RawBytes: 400}, nil
}

type fakeVerify struct {
	failures []string
	calls    int
}

func (f *fakeVerify) Run(ctx context.Context, m *manifest.Manifest) *verify.Report {
	f.calls++
	return &verify.Report{Backup: m, Passed: len(f.failures) == 0, Failures: f.failures, Total: time.Second}
}

type recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recorder) Notify(ctx context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, text)
	return nil
}

func newJob(b Backuper, v Verifier) (*Job, *recorder, *metrics.Metrics) {
	rec := &recorder{}
	m := metrics.New()
	return &Job{
		Backup: b, Verify: v, Metrics: m, Notifier: rec,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	}, rec, m
}

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestJobSuccess(t *testing.T) {
	v := &fakeVerify{}
	pruned := 0
	job, rec, m := newJob(&fakeBackup{}, v)
	job.Prune = func(context.Context) (int, error) { pruned++; return 2, nil }

	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v.calls != 1 || pruned != 1 || len(rec.msgs) != 0 {
		t.Errorf("verify=%d pruned=%d notifications=%v", v.calls, pruned, rec.msgs)
	}
	out := scrape(t, m)
	for _, want := range []string{"pg_backup_last_size_bytes 100", "pg_backup_last_verify_success 1", `pg_backup_runs_total{result="success"} 1`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestJobBackupFailureNotifiesAndSkipsTheRest(t *testing.T) {
	v := &fakeVerify{}
	pruned := 0
	job, rec, m := newJob(&fakeBackup{err: errors.New("disk full")}, v)
	job.Prune = func(context.Context) (int, error) { pruned++; return 0, nil }

	if err := job.Run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if v.calls != 0 || pruned != 0 {
		t.Errorf("nothing should run after a failed backup: verify=%d prune=%d", v.calls, pruned)
	}
	if len(rec.msgs) != 1 || !strings.Contains(rec.msgs[0], "disk full") {
		t.Errorf("notifications: %v", rec.msgs)
	}
	if out := scrape(t, m); !strings.Contains(out, `pg_backup_runs_total{result="failure"} 1`) {
		t.Error("failure not counted")
	}
}

func TestJobVerifyFailureNotifiesAndKeepsOldBackups(t *testing.T) {
	v := &fakeVerify{failures: []string{"table public.orders: expected 5 rows, restored 4"}}
	pruned := 0
	job, rec, m := newJob(&fakeBackup{}, v)
	job.Prune = func(context.Context) (int, error) { pruned++; return 0, nil }

	if err := job.Run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if pruned != 0 {
		t.Error("must not prune after a failed drill: the old backups may be the only good ones")
	}
	if len(rec.msgs) != 1 || !strings.Contains(rec.msgs[0], "expected 5 rows, restored 4") {
		t.Errorf("notifications: %v", rec.msgs)
	}
	if out := scrape(t, m); !strings.Contains(out, "pg_backup_last_verify_success 0") {
		t.Error("verify gauge should be 0")
	}
}

func TestJobWithoutVerify(t *testing.T) {
	job, _, _ := newJob(&fakeBackup{}, nil)
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestJobPruneFailureNotifies(t *testing.T) {
	job, rec, _ := newJob(&fakeBackup{}, nil)
	job.Prune = func(context.Context) (int, error) { return 0, errors.New("bucket unreachable") }
	if err := job.Run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if len(rec.msgs) != 1 || !strings.Contains(rec.msgs[0], "bucket unreachable") {
		t.Errorf("notifications: %v", rec.msgs)
	}
}

func TestValidateSchedule(t *testing.T) {
	for _, ok := range []string{"0 3 * * *", "*/5 * * * *", "@daily"} {
		if err := ValidateSchedule(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "every day", "99 * * * *", "0 3 * *"} {
		if err := ValidateSchedule(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestServeRunsOnStartAndStops(t *testing.T) {
	b := &fakeBackup{}
	job, _, _ := newJob(b, nil)
	ctx, cancel := context.WithCancel(context.Background())
	addr := freeAddr(t)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, Options{Schedule: "0 3 * * *", Listen: addr, RunNow: true}, job) }()

	waitFor(t, func() bool {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	waitFor(t, func() bool {
		return strings.Contains(scrapeURL(t, "http://"+addr+"/metrics"), `pg_backup_runs_total{result="success"} 1`)
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

// slowVerify stands in for a drill: when ctx is cancelled it still needs a
// moment to drop its scratch database.
type slowVerify struct {
	started chan struct{}
	cleaned atomic.Bool
}

func (f *slowVerify) Run(ctx context.Context, m *manifest.Manifest) *verify.Report {
	close(f.started)
	<-ctx.Done()
	time.Sleep(300 * time.Millisecond)
	f.cleaned.Store(true)
	return &verify.Report{Backup: m, Failures: []string{"cancelled"}}
}

func TestServeWaitsForTheFirstRunToFinishItsCleanup(t *testing.T) {
	v := &slowVerify{started: make(chan struct{})}
	job, _, _ := newJob(&fakeBackup{}, v)
	ctx, cancel := context.WithCancel(context.Background())
	addr := freeAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{Schedule: "0 3 * * *", Listen: addr, RunNow: true}, job)
	}()

	select {
	case <-v.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the drill never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if !v.cleaned.Load() {
		t.Error("Serve returned before the drill finished its cleanup")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func scrapeURL(t *testing.T, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// A client that does not keep connections open, so shutdown is not held up by it.
var client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 3 * time.Second}
