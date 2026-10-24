package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ramaraihan12/pg-backup/internal/config"
	"github.com/ramaraihan12/pg-backup/internal/demo"
	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/pg"
	"github.com/ramaraihan12/pg-backup/internal/retention"
	"github.com/ramaraihan12/pg-backup/internal/store"
)

type checkSpec struct{ name, sql string }

func (c checkSpec) toConfig() config.Check { return config.Check{Name: c.name, SQL: c.sql} }

func readObject(t *testing.T, s store.Store, key string) []byte {
	t.Helper()
	rc, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackupListVerify(t *testing.T) {
	p := demo.Small()
	e := newEnv(t, p)
	ctx := context.Background()

	m, err := e.backuper(nil).Run(ctx)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	// The manifest has a count for each of the four tables and matches the database.
	if len(m.Tables) != 4 {
		t.Fatalf("tables in manifest: %+v", m.Tables)
	}
	want := map[string]int64{
		"public.customers": int64(p.Customers), "public.products": int64(p.Products), "public.orders": int64(p.Orders),
	}
	for _, tb := range m.Tables {
		if w, ok := want[tb.Qualified()]; ok && tb.Rows != w {
			t.Errorf("%s: %d rows in manifest, want %d", tb.Qualified(), tb.Rows, w)
		}
	}
	if m.ServerVersion == "" || m.PgDumpVersion == "" || m.RawBytes <= m.StoredBytes {
		t.Errorf("manifest looks wrong: %+v", m)
	}

	// The stored object is neither plaintext nor a pg_dump archive.
	obj := readObject(t, e.store, m.Object)
	if int64(len(obj)) != m.StoredBytes {
		t.Errorf("object is %d bytes, manifest says %d", len(obj), m.StoredBytes)
	}
	if bytes.Contains(obj, []byte("contoh.test")) || bytes.HasPrefix(obj, []byte("PGDMP")) {
		t.Error("stored object is not encrypted")
	}

	l, err := e.cat.List(ctx)
	if err != nil || len(l.Backups) != 1 || len(l.Problems) != 0 || l.Backups[0].ID != m.ID {
		t.Fatalf("list = %+v, %v", l, err)
	}

	rep := e.drill(checkSpec{"orders point at customers",
		"SELECT count(*) = 0 FROM orders o LEFT JOIN customers c ON c.id = o.customer_id WHERE c.id IS NULL"},
	).Run(ctx, m)
	if !rep.Passed {
		t.Fatalf("verify failed: %v", rep.Failures)
	}
	if len(rep.Tables) != 4 || len(rep.Checks) != 1 || !rep.Checks[0].OK {
		t.Errorf("report: %+v", rep)
	}
	if e.databaseExists(rep.Scratch) {
		t.Errorf("scratch database %s was not dropped", rep.Scratch)
	}
}

func TestFailingSQLCheckFailsVerify(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	m, err := e.backuper(nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rep := e.drill(
		checkSpec{"fine", "SELECT true"},
		checkSpec{"impossible", "SELECT count(*) = 0 FROM orders"},
		checkSpec{"not a boolean", "SELECT 'oops'"},
	).Run(ctx, m)
	if rep.Passed {
		t.Fatal("verify should fail when a check fails")
	}
	if len(rep.Checks) != 3 || !rep.Checks[0].OK || rep.Checks[1].OK || rep.Checks[2].OK {
		t.Errorf("checks: %+v", rep.Checks)
	}
	if e.databaseExists(rep.Scratch) {
		t.Error("scratch database should be dropped even when checks fail")
	}
}

func TestCorruptedObjectFailsVerify(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"flipped byte": func(b []byte) []byte {
			out := append([]byte(nil), b...)
			out[len(out)/2] ^= 0xff
			return out
		},
		"truncated": func(b []byte) []byte { return b[:len(b)-200] },
		"appended garbage": func(b []byte) []byte {
			return append(append([]byte(nil), b...), []byte("tail")...)
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, demo.Small())
			ctx := context.Background()
			m, err := e.backuper(nil).Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			good := readObject(t, e.store, m.Object)
			if err := e.store.Put(ctx, m.Object, bytes.NewReader(damage(good))); err != nil {
				t.Fatal(err)
			}

			rep := e.drill().Run(ctx, m)
			if rep.Passed {
				t.Fatal("verify passed on a damaged object")
			}
			if !strings.Contains(strings.Join(rep.Failures, "\n"), "checksum mismatch") {
				t.Errorf("failures should say checksum mismatch: %v", rep.Failures)
			}
			if e.databaseExists(rep.Scratch) {
				t.Error("scratch database should be dropped after a failed restore")
			}
		})
	}
}

func TestMissingObjectFailsVerify(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	m, err := e.backuper(nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Delete(ctx, m.Object); err != nil {
		t.Fatal(err)
	}
	rep := e.drill().Run(ctx, m)
	if rep.Passed || !strings.Contains(strings.Join(rep.Failures, "\n"), "not found") {
		t.Errorf("want a not found failure, got %v", rep.Failures)
	}
}

func TestTamperedManifestCountsFailVerify(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	m, err := e.backuper(nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend the backup captured one more order than the dump holds.
	for i := range m.Tables {
		if m.Tables[i].Name == "orders" {
			m.Tables[i].Rows++
		}
	}
	rep := e.drill().Run(ctx, m)
	if rep.Passed || !strings.Contains(strings.Join(rep.Failures, "\n"), "public.orders") {
		t.Errorf("want a row count failure for orders, got %v", rep.Failures)
	}
}

func TestRowCountsMatchTheDumpWhileWritesContinue(t *testing.T) {
	p := demo.Small()
	p.Orders = 20_000 // big enough that the dump takes a moment
	e := newEnv(t, p)
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, e.srcURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	var stop atomic.Bool
	var inserted atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		id := int64(1_000_000)
		for !stop.Load() {
			id++
			if _, err := conn.Exec(ctx, `INSERT INTO orders (id, customer_id, status, ordered_at, payment_method,
				shipping_address, note, total_idr) VALUES ($1, 1, 'selesai', now(), 'qris', 'x', '', 1000)`, id); err != nil {
				return
			}
			inserted.Add(1)
		}
	}()

	m, err := e.backuper(nil).Run(ctx)
	stop.Store(true)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if inserted.Load() == 0 {
		t.Skip("the writer did not manage to insert anything during the backup")
	}

	rep := e.drill().Run(ctx, m)
	if !rep.Passed {
		t.Fatalf("counts should match the dump even though %d rows were inserted meanwhile: %v", inserted.Load(), rep.Failures)
	}
}

func TestRestoreIntoTarget(t *testing.T) {
	p := demo.Small()
	e := newEnv(t, p)
	ctx := context.Background()
	m, err := e.backuper(nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}

	target := "restored_" + fmt.Sprint(time.Now().UnixNano()%1_000_000)
	if err := pg.CreateDatabase(ctx, e.stack.pgAdminURL, target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DropDatabase(context.Background(), e.stack.pgAdminURL, target) })
	targetURL, _ := pg.WithDatabase(e.stack.pgAdminURL, target)

	r := e.drill().Restorer
	if err := r.Restore(ctx, m, targetURL, pg.RestoreOptions{NoOwner: true}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := pg.CountRestored(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, tb := range got {
		total += tb.Rows
	}
	if total != m.Rows() {
		t.Errorf("restored %d rows, manifest says %d", total, m.Rows())
	}

	// Restoring over the same data again runs in one transaction and must fail cleanly.
	if err := r.Restore(ctx, m, targetURL, pg.RestoreOptions{NoOwner: true}); err == nil {
		t.Error("restoring into a database that already has the tables should fail")
	}
	again, _ := pg.CountRestored(ctx, targetURL)
	var total2 int64
	for _, tb := range again {
		total2 += tb.Rows
	}
	if total2 != total {
		t.Errorf("failed restore changed the database: %d rows, was %d", total2, total)
	}
}

func TestRestoreWithWrongKeyFails(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	m, err := e.backuper(nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := e.drill()
	other := newEnv2Key(t)
	d.Restorer.Identities = other
	rep := d.Run(ctx, m)
	if rep.Passed {
		t.Fatal("verify passed with the wrong key")
	}
	if strings.Contains(strings.Join(rep.Failures, "\n"), "checksum mismatch") {
		t.Errorf("a wrong key is not corruption: %v", rep.Failures)
	}
}

func TestPruneRemovesTheRightObjects(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()

	// Five backups, one per day, ending 2026-10-05.
	var ids []string
	for day := 1; day <= 5; day++ {
		ts := time.Date(2026, 10, day, 3, 0, 0, 0, time.UTC)
		m, err := e.backuper(func() time.Time { return ts }).Run(ctx)
		if err != nil {
			t.Fatalf("backup %d: %v", day, err)
		}
		ids = append(ids, m.ID)
	}
	// An unrelated object under the same prefix must survive.
	if err := e.store.Put(ctx, "it/notes.txt", strings.NewReader("keep me")); err != nil {
		t.Fatal(err)
	}

	policy := retention.Policy{Daily: 2}
	entries, err := e.cat.Prune(ctx, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := countObjects(t, e.store); n != 11 {
		t.Fatalf("dry run changed the bucket: %d objects", n)
	}
	var wouldDelete []string
	for _, en := range entries {
		if !en.Keep {
			wouldDelete = append(wouldDelete, en.Backup.ID)
		}
	}
	if len(wouldDelete) != 3 {
		t.Fatalf("dry run would delete %v", wouldDelete)
	}

	if _, err := e.cat.Prune(ctx, policy, true); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	objs, _ := e.store.List(ctx, "")
	for _, o := range objs {
		left[o.Key] = true
	}
	for i, id := range ids {
		keep := i >= 3 // days 4 and 5
		for _, key := range []string{manifest.ObjectKey("it", e.srcName, id), manifest.ManifestKey("it", e.srcName, id)} {
			if left[key] != keep {
				t.Errorf("%s: exists=%v, want %v", key, left[key], keep)
			}
		}
	}
	if !left["it/notes.txt"] {
		t.Error("prune deleted an object that is not a backup")
	}
}

func countObjects(t *testing.T, s store.Store) int {
	t.Helper()
	objs, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return len(objs)
}

func TestFailedBackupLeavesNothingBehind(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	b := e.backuper(nil)
	b.DatabaseURL, _ = pg.WithDatabase(e.srcURL, "does_not_exist")
	if _, err := b.Run(ctx); err == nil {
		t.Fatal("expected the backup to fail")
	}
	if n := countObjects(t, e.store); n != 0 {
		t.Errorf("%d objects left after a failed backup", n)
	}
	l, err := e.cat.List(ctx)
	if err != nil || len(l.Backups) != 0 {
		t.Errorf("list = %+v, %v", l, err)
	}
}

func TestS3Store(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	s := e.store

	if _, err := s.Get(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of a missing key = %v", err)
	}
	// Bigger than one 16 MiB part, to exercise multipart upload with unknown length.
	payload := bytes.Repeat([]byte("0123456789abcdef"), 2<<20) // 32 MiB
	if err := s.Put(ctx, "big/object", bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if got := readObject(t, s, "big/object"); !bytes.Equal(got, payload) {
		t.Error("multipart round trip changed the bytes")
	}
	list, err := s.List(ctx, "big/")
	if err != nil || len(list) != 1 || list[0].Size != int64(len(payload)) {
		t.Errorf("list = %+v, %v", list, err)
	}
	if err := s.Delete(ctx, "big/object"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "big/object"); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
}

func TestSameSecondBackupIsRefused(t *testing.T) {
	e := newEnv(t, demo.Small())
	ctx := context.Background()
	at := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	b := e.backuper(func() time.Time { return at })
	first, err := b.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Run(ctx); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second backup in the same second = %v, want an 'already exists' error", err)
	}
	// The first backup must be untouched.
	l, err := e.cat.List(ctx)
	if err != nil || len(l.Backups) != 1 || l.Backups[0].Checksum != first.Checksum || len(l.Problems) != 0 {
		t.Errorf("list = %+v, %v", l, err)
	}
}
