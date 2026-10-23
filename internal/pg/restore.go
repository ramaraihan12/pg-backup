package pg

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RestoreOptions tunes pg_restore.
type RestoreOptions struct {
	// NoOwner skips ownership commands. Needed when the roles in the dump do
	// not exist on the target.
	NoOwner bool
	// NoPrivileges skips GRANT and REVOKE.
	NoPrivileges bool
}

// Restore feeds a custom-format dump from r into the existing database in
// dsn. It runs in one transaction, so a failure leaves the database as it was.
func (t Tools) Restore(ctx context.Context, dsn string, r io.Reader, o RestoreOptions) error {
	pgRestore, err := t.bin("pg_restore")
	if err != nil {
		return err
	}
	env, err := connEnv(dsn)
	if err != nil {
		return err
	}
	// pg_restore does not read PGDATABASE, so the name goes on the command line.
	// The rest of the connection (host, user, password) comes from the environment.
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("bad database url: %w", err)
	}
	args := []string{"--single-transaction", "--exit-on-error", "--dbname=" + strings.TrimPrefix(u.Path, "/")}
	if o.NoOwner {
		args = append(args, "--no-owner")
	}
	if o.NoPrivileges {
		args = append(args, "--no-privileges")
	}
	cmd := exec.CommandContext(ctx, pgRestore, args...)
	cmd.Env = env
	cmd.Stdin = r
	stderr := &tail{n: 4096}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return cmdError("pg_restore", err, stderr)
	}
	return nil
}

// CreateDatabase creates an empty database on the server in adminDSN.
func CreateDatabase(ctx context.Context, adminDSN, name string) error {
	return adminExec(ctx, adminDSN, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
}

// DropDatabase drops a database, disconnecting anyone still in it.
func DropDatabase(ctx context.Context, adminDSN, name string) error {
	return adminExec(ctx, adminDSN, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
}

// adminExec runs one statement against the maintenance database.
func adminExec(ctx context.Context, adminDSN, sql string) error {
	dsn, err := WithDatabase(adminDSN, "postgres")
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, sql); err != nil {
		return err
	}
	return nil
}
