package backup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/retention"
	"github.com/ramaraihan12/pg-backup/internal/store"
)

const (
	prefix = "pg-backup"
	dbName = "toko_maju"
)

// seed writes a fake backup (object plus manifest) at the given time.
func seed(t *testing.T, s store.Store, when string) *manifest.Manifest {
	t.Helper()
	ts, err := time.Parse("2006-01-02 15:04", when)
	if err != nil {
		t.Fatal(err)
	}
	id := manifest.NewID(ts)
	m := &manifest.Manifest{
		Version: manifest.SchemaVersion, ID: id, Database: dbName,
		ServerVersion: "17.6", StartedAt: ts, FinishedAt: ts.Add(time.Minute),
		Object:      manifest.ObjectKey(prefix, dbName, id),
		StoredBytes: 5, RawBytes: 9,
		Checksum: strings.Repeat("c", 64),
		Tables:   []manifest.Table{{Schema: "public", Name: "orders", Rows: 3}},
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, m.Object, strings.NewReader("dump!")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, manifest.ManifestKey(prefix, dbName, id), strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
	return m
}

func keys(t *testing.T, s store.Store) map[string]bool {
	t.Helper()
	objs, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, o := range objs {
		out[o.Key] = true
	}
	return out
}

func TestListNewestFirst(t *testing.T) {
	s := store.NewMemory()
	seed(t, s, "2026-10-01 03:00")
	seed(t, s, "2026-10-03 03:00")
	seed(t, s, "2026-10-02 03:00")
	// A different database under the same prefix must not show up.
	if err := s.Put(context.Background(), "pg-backup/other/other-20261004T030000Z.manifest.json", strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}

	c := Catalog{Store: s, Prefix: prefix, Database: dbName}
	l, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Backups) != 3 || len(l.Problems) != 0 {
		t.Fatalf("backups=%d problems=%v", len(l.Backups), l.Problems)
	}
	if l.Backups[0].ID != "20261003T030000Z" || l.Backups[2].ID != "20261001T030000Z" {
		t.Errorf("order: %s %s %s", l.Backups[0].ID, l.Backups[1].ID, l.Backups[2].ID)
	}
}

func TestListReportsProblems(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	good := seed(t, s, "2026-10-01 03:00")
	gone := seed(t, s, "2026-10-02 03:00")
	if err := s.Delete(ctx, gone.Object); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, manifest.ManifestKey(prefix, dbName, "20261003T030000Z"), strings.NewReader("garbage")); err != nil {
		t.Fatal(err)
	}
	// A manifest that points at some other object must be ignored, because prune would delete it.
	evil := seed(t, s, "2026-10-04 03:00")
	evil.Object = "pg-backup/other/precious.dump"
	data, _ := evil.Marshal()
	if err := s.Put(ctx, manifest.ManifestKey(prefix, dbName, evil.ID), strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}

	l, err := (Catalog{Store: s, Prefix: prefix, Database: dbName}).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Backups) != 2 {
		t.Fatalf("want the good one and the one with a missing object, got %d", len(l.Backups))
	}
	if l.Backups[1].ID != good.ID {
		t.Errorf("unexpected backups: %+v", l.Backups)
	}
	joined := strings.Join(l.Problems, "\n")
	for _, want := range []string{"is missing", "parse manifest", "does not match its own key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems should mention %q:\n%s", want, joined)
		}
	}
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	c := Catalog{Store: s, Prefix: prefix, Database: dbName}

	if _, err := c.Resolve(ctx, "latest"); !errors.Is(err, ErrNoBackups) {
		t.Fatalf("empty bucket: %v", err)
	}
	seed(t, s, "2026-10-01 03:00")
	seed(t, s, "2026-10-02 03:00")

	for _, id := range []string{"", "latest"} {
		m, err := c.Resolve(ctx, id)
		if err != nil || m.ID != "20261002T030000Z" {
			t.Errorf("Resolve(%q) = %v, %v", id, m, err)
		}
	}
	if m, err := c.Resolve(ctx, "20261001T030000Z"); err != nil || m.ID != "20261001T030000Z" {
		t.Errorf("Resolve by id = %v, %v", m, err)
	}
	if _, err := c.Resolve(ctx, "20250101T000000Z"); err == nil {
		t.Error("unknown id should fail")
	}
}

func TestPruneDryRunDeletesNothing(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	for _, w := range []string{"2026-10-01 03:00", "2026-10-02 03:00", "2026-10-03 03:00"} {
		seed(t, s, w)
	}
	before := len(keys(t, s))

	entries, err := (Catalog{Store: s, Prefix: prefix, Database: dbName}).Prune(ctx, retention.Policy{Daily: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	drop := 0
	for _, e := range entries {
		if !e.Keep {
			drop++
		}
		if e.Deleted {
			t.Error("dry run reported a deletion")
		}
	}
	if drop != 2 {
		t.Errorf("would drop %d, want 2", drop)
	}
	if len(keys(t, s)) != before {
		t.Error("dry run changed the bucket")
	}
}

func TestPruneApplyDeletesRightObjects(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	oldest := seed(t, s, "2026-08-01 03:00")
	mid := seed(t, s, "2026-10-01 03:00")
	newest := seed(t, s, "2026-10-02 03:00")
	// Not ours: another database and a stray file under our prefix.
	if err := s.Put(ctx, "pg-backup/other/other-20260801T030000Z.dump.zst.age", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "pg-backup/toko_maju/README.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}

	entries, err := (Catalog{Store: s, Prefix: prefix, Database: dbName}).Prune(ctx, retention.Policy{Daily: 1, Monthly: 2}, true)
	if err != nil {
		t.Fatal(err)
	}
	var deleted []string
	for _, e := range entries {
		if e.Err != nil {
			t.Errorf("%s: %v", e.Backup.ID, e.Err)
		}
		if e.Deleted {
			deleted = append(deleted, e.Backup.ID)
		}
	}
	// Daily 1 keeps newest. Monthly 2 keeps the newest of Oct (same one) and Aug.
	if len(deleted) != 1 || deleted[0] != mid.ID {
		t.Fatalf("deleted %v, want only %s", deleted, mid.ID)
	}

	left := keys(t, s)
	for _, k := range []string{
		oldest.Object, manifest.ManifestKey(prefix, dbName, oldest.ID),
		newest.Object, manifest.ManifestKey(prefix, dbName, newest.ID),
		"pg-backup/other/other-20260801T030000Z.dump.zst.age", "pg-backup/toko_maju/README.txt",
	} {
		if !left[k] {
			t.Errorf("%s should still exist", k)
		}
	}
	for _, k := range []string{mid.Object, manifest.ManifestKey(prefix, dbName, mid.ID)} {
		if left[k] {
			t.Errorf("%s should be gone", k)
		}
	}
}

func TestPruneRejectsEmptyPolicy(t *testing.T) {
	s := store.NewMemory()
	seed(t, s, "2026-10-01 03:00")
	if _, err := (Catalog{Store: s, Prefix: prefix, Database: dbName}).Prune(context.Background(), retention.Policy{}, true); err == nil {
		t.Fatal("an empty policy must not be allowed to delete everything")
	}
	if len(keys(t, s)) != 2 {
		t.Error("objects were deleted")
	}
}
