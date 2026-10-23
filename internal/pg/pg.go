// Package pg wraps the Postgres client tools and the few queries pg-backup needs.
package pg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Tools locates pg_dump and pg_restore.
type Tools struct {
	// BinDir is searched first. Empty means use PATH.
	BinDir string
}

func (t Tools) bin(name string) (string, error) {
	if t.BinDir != "" {
		p := filepath.Join(t.BinDir, name)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s not found in %s", name, t.BinDir)
		}
		return p, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH: install the Postgres 17 client tools or set postgres.bin_dir", name)
	}
	return p, nil
}

// Version returns the version number of a client tool, for example "17.11".
func (t Tools) Version(ctx context.Context, name string) (string, error) {
	p, err := t.bin(name)
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, p, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", name, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("unexpected output from %s --version", name)
	}
	return fields[len(fields)-1], nil
}

// connEnv turns a postgres:// URL into PG* environment variables, so the
// password never shows up in the process list.
func connEnv(dsn string) ([]string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("bad database url: %w", err)
	}
	env := os.Environ()
	set := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	set("PGHOST", u.Hostname())
	set("PGPORT", u.Port())
	if u.User != nil {
		set("PGUSER", u.User.Username())
		if pw, ok := u.User.Password(); ok {
			set("PGPASSWORD", pw)
		}
	}
	set("PGDATABASE", strings.TrimPrefix(u.Path, "/"))
	q := u.Query()
	set("PGSSLMODE", q.Get("sslmode"))
	set("PGSSLROOTCERT", q.Get("sslrootcert"))
	set("PGCONNECT_TIMEOUT", q.Get("connect_timeout"))
	return env, nil
}

// WithDatabase returns dsn pointing at another database on the same server.
func WithDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("bad database url: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// tail keeps the last n bytes written to it, for error messages.
type tail struct {
	n   int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = t.buf[len(t.buf)-t.n:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(bytes.TrimSpace(t.buf)) }

func cmdError(name string, err error, stderr *tail) error {
	if msg := stderr.String(); msg != "" {
		return fmt.Errorf("%s failed: %w: %s", name, err, msg)
	}
	return fmt.Errorf("%s failed: %w", name, err)
}

// Quote quotes a schema-qualified table name for use in SQL.
func Quote(schema, name string) string {
	return pgx.Identifier{schema, name}.Sanitize()
}

var errNoRows = errors.New("query returned no rows")
