// Package integration runs pg-backup against a real Postgres 17 and a real
// MinIO, both started with testcontainers. `go test -short` skips all of it.
//
// It needs Docker and the Postgres 17 client tools (pg_dump, pg_restore) on
// PATH, or POSTGRES_BIN_DIR pointing at them.
package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ramaraihan12/pg-backup/internal/backup"
	"github.com/ramaraihan12/pg-backup/internal/demo"
	"github.com/ramaraihan12/pg-backup/internal/pg"
	"github.com/ramaraihan12/pg-backup/internal/store"
	"github.com/ramaraihan12/pg-backup/internal/verify"
)

// MinIO stopped publishing images under minio/minio, so the tests use a
// pinned build from another publisher.
const (
	postgresImage = "postgres:17"
	minioImage    = "chainguard/minio@sha256:d88920d6a50bd0b088223b688be3b76c63e6205dbe494437f67ba1240ea12880"
)

type stack struct {
	pgAdminURL string // points at the "postgres" database
	s3Endpoint string
	s3User     string
	s3Pass     string
}

var (
	once      sync.Once
	shared    *stack
	sharedErr error
	cleanups  []func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, c := range cleanups {
		c()
	}
	os.Exit(code)
}

// getStack starts the containers on first use and shares them between tests.
func getStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker, skipped with -short")
	}
	once.Do(func() { shared, sharedErr = startStack() })
	if sharedErr != nil {
		t.Fatalf("start containers: %v", sharedErr)
	}
	return shared
}

func startStack() (*stack, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pgc, err := postgres.Run(ctx, postgresImage,
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if pgc != nil {
		cleanups = append(cleanups, func() { _ = testcontainers.TerminateContainer(pgc) })
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	pgURL, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}

	mc, err := minio.Run(ctx, minioImage, minio.WithUsername("pgbackup"), minio.WithPassword("pgbackup-secret"))
	if mc != nil {
		cleanups = append(cleanups, func() { _ = testcontainers.TerminateContainer(mc) })
	}
	if err != nil {
		return nil, fmt.Errorf("minio: %w", err)
	}
	endpoint, err := mc.ConnectionString(ctx)
	if err != nil {
		return nil, err
	}
	return &stack{pgAdminURL: pgURL, s3Endpoint: endpoint, s3User: mc.Username, s3Pass: mc.Password}, nil
}

// env is one test's private slice of the stack: its own source database, its
// own bucket and its own age key.
type env struct {
	t       *testing.T
	stack   *stack
	srcName string
	srcURL  string
	store   *store.S3
	cat     backup.Catalog
	tools   pg.Tools
	id      *age.X25519Identity
}

func newEnv(t *testing.T, p demo.Params) *env {
	t.Helper()
	s := getStack(t)
	ctx := context.Background()
	name := "src_" + strings.ToLower(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))
	if len(name) > 60 {
		name = name[:60]
	}

	if err := pg.CreateDatabase(ctx, s.pgAdminURL, name); err != nil {
		t.Fatalf("create source database: %v", err)
	}
	t.Cleanup(func() { _ = pg.DropDatabase(context.Background(), s.pgAdminURL, name) })
	srcURL, _ := pg.WithDatabase(s.pgAdminURL, name)

	conn, err := pgx.Connect(ctx, srcURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if err := demo.Generate(ctx, conn, p, nil); err != nil {
		t.Fatalf("generate demo data: %v", err)
	}

	st, err := store.NewS3(store.S3Options{
		Endpoint: s.s3Endpoint, Bucket: bucketName(t), Region: "us-east-1",
		AccessKey: s.s3User, SecretKey: s.s3Pass,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return &env{
		t: t, stack: s, srcName: name, srcURL: srcURL, store: st, id: id,
		cat:   backup.Catalog{Store: st, Prefix: "it", Database: name},
		tools: pg.Tools{BinDir: os.Getenv("POSTGRES_BIN_DIR")},
	}
}

func bucketName(t *testing.T) string {
	n := "t-" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	if len(n) > 60 {
		n = n[:60]
	}
	return strings.TrimRight(n, "-")
}

func (e *env) backuper(now func() time.Time) *backup.Backuper {
	return &backup.Backuper{
		Catalog: e.cat, Tools: e.tools, Recipients: []age.Recipient{e.id.Recipient()},
		DatabaseURL: e.srcURL, Now: now,
	}
}

func (e *env) drill(checks ...checkSpec) *verify.Drill {
	d := &verify.Drill{
		Restorer:   &backup.Restorer{Catalog: e.cat, Tools: e.tools, Identities: []age.Identity{e.id}},
		ScratchURL: e.stack.pgAdminURL,
	}
	for _, c := range checks {
		d.Checks = append(d.Checks, c.toConfig())
	}
	return d
}

func (e *env) databaseExists(name string) bool {
	e.t.Helper()
	conn, err := pgx.Connect(context.Background(), e.stack.pgAdminURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var n int
	if err := conn.QueryRow(context.Background(), "SELECT count(*) FROM pg_database WHERE datname = $1", name).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n == 1
}

// newEnv2Key makes an unrelated age identity, for wrong-key tests.
func newEnv2Key(t *testing.T) []age.Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return []age.Identity{id}
}
