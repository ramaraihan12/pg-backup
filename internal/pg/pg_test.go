package pg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func envMap(t *testing.T, dsn string) map[string]string {
	t.Helper()
	env, err := connEnv(dsn)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v // later entries win, like exec does
	}
	return m
}

func TestConnEnv(t *testing.T) {
	m := envMap(t, "postgres://backup:p%40ss@db.internal:5433/toko_maju?sslmode=require&connect_timeout=5")
	want := map[string]string{
		"PGHOST": "db.internal", "PGPORT": "5433", "PGUSER": "backup",
		"PGPASSWORD": "p@ss", "PGDATABASE": "toko_maju",
		"PGSSLMODE": "require", "PGCONNECT_TIMEOUT": "5",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
}

func TestConnEnvPasswordNotInArgs(t *testing.T) {
	// The password is passed by environment, so the dsn is never a command line argument.
	m := envMap(t, "postgres://u:secret@h/d")
	if m["PGPASSWORD"] != "secret" {
		t.Fatal("password missing from env")
	}
}

func TestWithDatabase(t *testing.T) {
	got, err := WithDatabase("postgres://u:p@h:5432/toko_maju?sslmode=disable", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://u:p@h:5432/postgres?sslmode=disable" {
		t.Errorf("got %s", got)
	}
}

func TestQuote(t *testing.T) {
	if got := Quote("public", `we"ird`); got != `"public"."we""ird"` {
		t.Errorf("got %s", got)
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	tl := &tail{n: 6}
	tl.Write([]byte("hello "))
	tl.Write([]byte("world\n"))
	if tl.String() != "world" {
		t.Errorf("got %q", tl.String())
	}
}

func TestToolsMissingBinary(t *testing.T) {
	dir := t.TempDir()
	_, err := (Tools{BinDir: dir}).bin("pg_dump")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The error names the file it looked for and where.
	if exe := exeName(runtime.GOOS, "pg_dump"); !strings.Contains(err.Error(), exe+" not found in "+dir) {
		t.Errorf("error %q does not name %s in %s", err, exe, dir)
	}
}

func TestExeName(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "pg_dump.exe",
		"linux":   "pg_dump",
		"darwin":  "pg_dump",
	} {
		if got := exeName(goos, "pg_dump"); got != want {
			t.Errorf("exeName(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestToolsFindsBinaryInBinDir(t *testing.T) {
	// pg_dump.exe on Windows, pg_dump everywhere else.
	dir := t.TempDir()
	want := filepath.Join(dir, exeName(runtime.GOOS, "pg_dump"))
	if err := os.WriteFile(want, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (Tools{BinDir: dir}).bin("pg_dump")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestShortVersion(t *testing.T) {
	for in, want := range map[string]string{
		"17.11 (Debian 17.11-1.pgdg12+2)": "17.11",
		"16.4":                            "16.4",
		"":                                "",
	} {
		if got := shortVersion(in); got != want {
			t.Errorf("shortVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
