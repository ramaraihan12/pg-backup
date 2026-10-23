package pg

import (
	"context"
	"fmt"

	"github.com/ramaraihan12/pg-backup/internal/manifest"

	"github.com/jackc/pgx/v5"
)

// querier is satisfied by *pgx.Conn and pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// User tables only: no catalogs, no extension-owned tables, and partitions are
// left out because their parent already counts their rows.
const listTablesSQL = `
SELECT n.nspname, c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp%'
  AND NOT EXISTS (
    SELECT 1 FROM pg_depend d
    WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
ORDER BY 1, 2`

// CountTables returns an exact row count for every user table.
func CountTables(ctx context.Context, q querier) ([]manifest.Table, error) {
	rows, err := q.Query(ctx, listTablesSQL)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	var tables []manifest.Table
	for rows.Next() {
		var t manifest.Table
		if err := rows.Scan(&t.Schema, &t.Name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	for i := range tables {
		t := &tables[i]
		if err := q.QueryRow(ctx, "SELECT count(*) FROM "+Quote(t.Schema, t.Name)).Scan(&t.Rows); err != nil {
			return nil, fmt.Errorf("count %s: %w", t.Qualified(), err)
		}
	}
	return tables, nil
}

// CountRestored connects to dsn and counts rows in every user table.
func CountRestored(ctx context.Context, dsn string) ([]manifest.Table, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background())
	return CountTables(ctx, conn)
}

// RunCheck runs one user-defined check inside a read-only transaction. The
// query must return a single boolean.
func RunCheck(ctx context.Context, dsn, sql string) (bool, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var ok bool
	if err := tx.QueryRow(ctx, sql).Scan(&ok); err != nil {
		if err == pgx.ErrNoRows {
			return false, errNoRows
		}
		return false, fmt.Errorf("the query must return one boolean: %w", err)
	}
	return ok, nil
}
