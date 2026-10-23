package manifest

import (
	"strings"
	"testing"
	"time"
)

func sample() *Manifest {
	start := time.Date(2026, 10, 23, 3, 15, 0, 0, time.UTC)
	return &Manifest{
		Version:         SchemaVersion,
		ID:              NewID(start),
		Database:        "toko_maju",
		ServerVersion:   "17.6",
		PgDumpVersion:   "17.11",
		StartedAt:       start,
		FinishedAt:      start.Add(42 * time.Second),
		DurationSeconds: 42,
		Object:          "pg-backup/toko_maju/toko_maju-20261023T031500Z.dump.zst.age",
		RawBytes:        1000,
		StoredBytes:     300,
		RawSHA256:       strings.Repeat("a", 64),
		Checksum:        strings.Repeat("b", 64),
		Tables: []Table{
			{Schema: "public", Name: "customers", Rows: 10},
			{Schema: "public", Name: "orders", Rows: 32},
		},
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	in := sample()
	data, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Checksum != in.Checksum || len(out.Tables) != 2 {
		t.Fatalf("round trip lost data: %+v", out)
	}
	if !out.StartedAt.Equal(in.StartedAt) {
		t.Errorf("started_at changed: %v vs %v", out.StartedAt, in.StartedAt)
	}
	if out.Rows() != 42 {
		t.Errorf("Rows() = %d, want 42", out.Rows())
	}
	if !out.Time().Equal(in.StartedAt) {
		t.Errorf("Time() = %v, want %v", out.Time(), in.StartedAt)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(m *Manifest){
		"wrong version":    func(m *Manifest) { m.Version = 99 },
		"bad id":           func(m *Manifest) { m.ID = "yesterday" },
		"no database":      func(m *Manifest) { m.Database = "" },
		"no object":        func(m *Manifest) { m.Object = "" },
		"short checksum":   func(m *Manifest) { m.Checksum = "abc" },
		"upper checksum":   func(m *Manifest) { m.Checksum = strings.Repeat("B", 64) },
		"zero stored size": func(m *Manifest) { m.StoredBytes = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sample()
			mutate(m)
			if err := m.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
			if _, err := m.Marshal(); err == nil {
				t.Fatal("Marshal should refuse an invalid manifest")
			}
		})
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Parse([]byte(`{"version":1}`)); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestIDs(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	id := NewID(time.Date(2026, 10, 23, 10, 15, 0, 0, loc))
	if id != "20261023T031500Z" {
		t.Fatalf("NewID should convert to UTC, got %s", id)
	}
	got, err := ParseID(id)
	if err != nil || !got.Equal(time.Date(2026, 10, 23, 3, 15, 0, 0, time.UTC)) {
		t.Fatalf("ParseID = %v, %v", got, err)
	}
	if _, err := ParseID("2026-10-23"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestKeys(t *testing.T) {
	id := "20261023T031500Z"
	if got := ObjectKey("pg-backup", "toko_maju", id); got != "pg-backup/toko_maju/toko_maju-20261023T031500Z.dump.zst.age" {
		t.Errorf("object key = %s", got)
	}
	mk := ManifestKey("pg-backup", "toko_maju", id)
	if mk != "pg-backup/toko_maju/toko_maju-20261023T031500Z.manifest.json" {
		t.Errorf("manifest key = %s", mk)
	}
	if Dir("", "toko_maju") != "toko_maju/" {
		t.Errorf("Dir with empty prefix = %s", Dir("", "toko_maju"))
	}

	got, ok := IDFromManifestKey("pg-backup", "toko_maju", mk)
	if !ok || got != id {
		t.Errorf("IDFromManifestKey = %q, %v", got, ok)
	}
	for _, key := range []string{
		ObjectKey("pg-backup", "toko_maju", id),
		"pg-backup/other/other-20261023T031500Z.manifest.json",
		"pg-backup/toko_maju/toko_maju-notanid.manifest.json",
		"elsewhere/toko_maju-20261023T031500Z.manifest.json",
	} {
		if _, ok := IDFromManifestKey("pg-backup", "toko_maju", key); ok {
			t.Errorf("%s should not be a manifest key", key)
		}
	}
}
