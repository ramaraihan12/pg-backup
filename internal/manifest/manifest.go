// Package manifest describes one backup and where its files live in the bucket.
//
// Every backup is two objects next to each other:
//
//	<prefix>/<database>/<database>-<id>.dump.zst.age
//	<prefix>/<database>/<database>-<id>.manifest.json
//
// The id is the UTC start time, for example 20261023T031500Z, so keys sort in
// time order.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// SchemaVersion is bumped when the JSON layout changes in a way old readers cannot handle.
const SchemaVersion = 1

const (
	idLayout       = "20060102T150405Z"
	objectSuffix   = ".dump.zst.age"
	manifestSuffix = ".manifest.json"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Table is the row count of one table at backup time.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
}

// Qualified returns schema.name.
func (t Table) Qualified() string { return t.Schema + "." + t.Name }

// Manifest is the JSON written next to each backup object.
type Manifest struct {
	Version         int       `json:"version"`
	ID              string    `json:"id"`
	Database        string    `json:"database"`
	ServerVersion   string    `json:"server_version"`
	PgDumpVersion   string    `json:"pg_dump_version"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationSeconds float64   `json:"duration_seconds"`
	Object          string    `json:"object"`
	RawBytes        int64     `json:"raw_bytes"`
	StoredBytes     int64     `json:"stored_bytes"`
	RawSHA256       string    `json:"raw_sha256"`
	// Checksum is the SHA-256 of the stored object (compressed and encrypted).
	Checksum string `json:"checksum"`
	// Tables holds exact row counts taken in the same snapshot pg_dump read.
	Tables []Table `json:"tables"`
}

// Rows is the total row count over all tables.
func (m *Manifest) Rows() int64 {
	var n int64
	for _, t := range m.Tables {
		n += t.Rows
	}
	return n
}

// Time is the backup start time parsed from the id.
func (m *Manifest) Time() time.Time {
	t, _ := ParseID(m.ID)
	return t
}

// Validate checks the fields the rest of the program relies on.
func (m *Manifest) Validate() error {
	if m.Version != SchemaVersion {
		return fmt.Errorf("unsupported manifest version %d (this build reads %d)", m.Version, SchemaVersion)
	}
	if _, err := ParseID(m.ID); err != nil {
		return err
	}
	if m.Database == "" {
		return errors.New("manifest has no database name")
	}
	if m.Object == "" {
		return errors.New("manifest has no object key")
	}
	if !sha256Hex.MatchString(m.Checksum) {
		return errors.New("manifest checksum is not a sha256 hex string")
	}
	if m.StoredBytes <= 0 {
		return errors.New("manifest stored_bytes must be positive")
	}
	return nil
}

// Marshal returns indented JSON with a trailing newline.
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Parse reads and validates a manifest.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// NewID formats a start time as a backup id.
func NewID(t time.Time) string { return t.UTC().Format(idLayout) }

// ParseID is the reverse of NewID.
func ParseID(id string) (time.Time, error) {
	t, err := time.Parse(idLayout, id)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad backup id %q (want something like 20261023T031500Z)", id)
	}
	return t, nil
}

// Dir is the key prefix that holds all backups of one database.
func Dir(prefix, database string) string {
	return path.Join(prefix, database) + "/"
}

// ObjectKey is the key of the encrypted dump.
func ObjectKey(prefix, database, id string) string {
	return path.Join(prefix, database, database+"-"+id+objectSuffix)
}

// ManifestKey is the key of the manifest JSON.
func ManifestKey(prefix, database, id string) string {
	return path.Join(prefix, database, database+"-"+id+manifestSuffix)
}

// IDFromManifestKey extracts the id from a manifest key of the given database.
func IDFromManifestKey(prefix, database, key string) (string, bool) {
	base := strings.TrimPrefix(key, Dir(prefix, database))
	if base == key || !strings.HasPrefix(base, database+"-") || !strings.HasSuffix(base, manifestSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(base, database+"-"), manifestSuffix)
	if _, err := ParseID(id); err != nil {
		return "", false
	}
	return id, true
}
