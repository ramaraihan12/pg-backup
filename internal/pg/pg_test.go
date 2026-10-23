package pg

import (
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
	if _, err := (Tools{BinDir: t.TempDir()}).bin("pg_dump"); err == nil {
		t.Fatal("expected an error")
	}
}
