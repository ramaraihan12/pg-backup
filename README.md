# pg-backup

[![ci](https://github.com/ramaraihan12/pg-backup/actions/workflows/ci.yml/badge.svg)](https://github.com/ramaraihan12/pg-backup/actions/workflows/ci.yml)

A small Go tool that streams `pg_dump` output through zstd and age into an S3-compatible bucket, keeps a grandfather-father-son retention policy, and proves each backup by restoring it into a scratch database. A backup you haven't restored is not a backup, so the restore drill is the main feature. It runs against MinIO by default, so trying it costs nothing.

All demo data is made up: an Indonesian shop called `toko_maju` with customers, products, orders and order items, produced by a generator in this repo. Its phone numbers all look like `+62 000-0000-0507`, which is not dialable, so none can match a real person.

## What it looks like

Real runs on my laptop (Ryzen 9 5900HX, 16 GB, Docker Desktop) against Postgres 17.11 and a local MinIO, both in containers. The database is 21 million made-up rows, 2.7 GiB on disk.

```console
$ pg-backup backup
backup 20261024T021450Z  demo_large (postgres 17.11)
  raw        1.3 GiB
  stored     333.9 MiB  (4.0x smaller, zstd + age)
  rows       21,252,906 in 4 tables
  took       14.0s
  peak rss   64.1 MiB
  checksum   767097b240a0d2a3...
  object     pg-backup/demo_large/demo_large-20261024T021450Z.dump.zst.age

$ pg-backup verify
verify 20261024T021450Z  demo_large, 333.9 MiB stored, 21,252,906 rows expected
  scratch database  pgbackup_verify_20261024t021450z_961b11
  restore           ok    79.6s
  row counts        ok    8.3s
    public.customers            1,250,000  ok
    public.order_items         15,000,906  ok
    public.orders               5,000,000  ok
    public.products                 2,000  ok
  check "every order has a customer"             ok
  check "order totals match their items"         ok
  cleanup           dropped  1.6s
PASS in 112.9s
```

Then I flipped one byte in the middle of a stored 6.5 MiB backup of a smaller database and ran verify. It stops at the checksum and still drops the scratch database:

```console
$ pg-backup verify    # after flipping one byte in the stored object
verify 20261024T021825Z  demo_medium, 6.5 MiB stored, 426,813 rows expected
  scratch database  pgbackup_verify_20261024t021825z_5d116d
  restore           FAIL  222ms
  cleanup           dropped  33ms
FAIL in 307ms
  - restore: checksum mismatch: stored sha256 is aadaf561d8b4abe96b034e6aa39e951c7c06e23ce956a63a9f71e2842597e2df, manifest says 213511b36abfa30bca481ee3fb200293f9e4ed3c761cf16e1f418c9cd8063374 (restore stopped with: pg_restore failed: exit status 1: pg_restore: error: could not read from input file: end of file)
error: restore drill failed
```

`prune` shows what it would do and deletes nothing without `--yes`. This bucket holds 73 backups that I faked by copying one real backup under older ids (about daily for 40 days, then every 5 days), with a policy of 7 daily, 4 weekly, 6 monthly (trimmed):

```console
$ pg-backup prune
dry run, nothing is deleted (add --yes to delete): keep 7 daily, 4 weekly, 6 monthly
  keep    20261024T021716Z  newest, daily 2026-10-24, weekly 2026-W43, monthly 2026-10
  (trimmed)
  keep    20261018T030000Z  daily 2026-10-18, weekly 2026-W42
  delete  20261017T030000Z  684.1 KiB
  (trimmed)
  keep    20260930T030000Z  monthly 2026-09
  delete  20260522T030000Z  684.1 KiB
  (trimmed)
14 kept, 59 would be deleted (39.4 MiB)
```

## How it works

- `backup` runs `pg_dump` in custom format and pipes it through zstd, then age, then a multipart upload (16 MiB parts). There is no temp file. SHA-256 is computed over the raw dump and over the stored bytes.
- Row counts in the manifest are exact. One repeatable-read transaction exports a snapshot, `pg_dump --snapshot` reads it, and every table is counted inside that same transaction, so the counts match the dump while writes continue.
- The manifest JSON is uploaded last. A backup without a manifest does not exist as far as `list` and `prune` are concerned, so a failed upload leaves nothing that looks valid. Ids have one-second resolution, and a backup whose id already exists is refused.
- `verify` downloads, decrypts and `pg_restore`s into a fresh `pgbackup_verify_*` database, compares row counts with the manifest, runs the read-only SQL checks from the config (each returns one boolean), then drops the database, even on failure or SIGTERM.
- `prune` keeps the newest backup of each of the last N days, ISO weeks and months, and always the newest one; it is a dry run unless you pass `--yes`. `serve` runs backup, verify and optionally prune on a cron schedule, exposes `/metrics`, and can message a Telegram bot on failure (off by default).

## Stack

Go 1.27.3, cobra 1.10, klauspost/compress 1.20 (zstd), age 1.3 (filippo.io/age), minio-go 7.4, pgx 5.11, robfig/cron 3, prometheus client_golang 1.25, testcontainers-go 0.44, staticcheck, PostgreSQL 17 client tools and MinIO.

## Run it

You need Docker. Go is only needed to build outside the container.

```bash
cp .env.example .env
docker run --rm $(docker build -q .) keygen     # paste both keys into .env
docker compose up -d
docker compose --profile demo run --rm seed     # optional: 20,000 made-up customers, 80,000 orders
docker compose exec pg-backup pg-backup backup
docker compose exec pg-backup pg-backup verify
```

Compose publishes Postgres (5442), MinIO (9010, console 9011) and metrics (9187) on `127.0.0.1` only. The `pg-backup` service runs `serve` with the schedule in `config.example.yaml` (03:00 daily); add `--run-now` to also run once at start. `curl -s localhost:9187/metrics | grep ^pg_backup` shows what that process has run, not the `exec` runs above.

Without Docker, build with `go build ./cmd/pg-backup`, copy `config.example.yaml` to `pg-backup.yaml` (it points at the compose database) and have `pg_dump` and `pg_restore` 17 on `PATH`. `list`, `prune --yes` and `restore --to <url> [--id <id>]` (into an existing, empty database) are the other commands.

Secrets come only from the environment: a `password:` key or a password inside a URL in the YAML is an error. Main settings, all overridable by env var (`PGBACKUP_DB_PASSWORD` fills a URL that has no password):

| Variable | Default | |
|---|---|---|
| `PGBACKUP_CONFIG`, `_DATABASE_URL`, `_VERIFY_URL`, `_PG_BIN_DIR` | YAML | config file, source and scratch URLs (a password is allowed here), dir of `pg_dump` |
| `PGBACKUP_S3_ENDPOINT`, `_BUCKET`, `_PREFIX`, `_REGION`, `_USE_SSL` | YAML | any S3-compatible store |
| `PGBACKUP_S3_ACCESS_KEY`, `_SECRET_KEY` | empty | required |
| `PGBACKUP_AGE_RECIPIENT`, `_AGE_IDENTITY` (or `_FILE`) | empty | public key to back up, private key to restore and verify |
| `PGBACKUP_RETENTION_DAILY`, `_WEEKLY`, `_MONTHLY` | 7, 4, 6 | |
| `PGBACKUP_SCHEDULE`, `_LISTEN`, `_SERVE_VERIFY`, `_SERVE_PRUNE` | `0 3 * * *`, `127.0.0.1:9187`, true, false | `serve` settings |
| `PGBACKUP_TELEGRAM`, `_TELEGRAM_TOKEN`, `_TELEGRAM_CHAT_ID` | off | failure alerts |

## Tests

```bash
go test -short ./...        # unit tests, no Docker
go test ./...               # adds the integration suite
```

The unit tests cover the retention rules (table-driven), the manifest, config loading, the metrics, the serve loop with fake backups, the Telegram notifier against a local HTTP server, and the stream pipeline (a flipped byte is a checksum mismatch). The integration suite starts Postgres 17 and MinIO with testcontainers and checks backup, list and verify, a corrupted object, a missing object, a tampered manifest, a failing SQL check, a wrong key, a failed backup leaving nothing behind, and prune. `-short` skips it. CI runs gofmt, `go vet`, staticcheck, the unit tests with `-race`, the integration suite and a Docker build. It installs Postgres 17 client tools because the runner ships 16 and `pg_dump` refuses a newer server.

## Numbers

Three made-up databases from the same generator, one backup each, same laptop. Peak RSS is `VmHWM` of the pg-backup process, read when it exits.

| Database rows | Raw dump | Stored (zstd + age) | Backup time | Peak RSS |
|---|---|---|---|---|
| 44,567 | 2.6 MiB | 684.1 KiB | 0.3s | 24.0 MiB |
| 426,813 | 25.6 MiB | 6.5 MiB | 0.7s | 45.4 MiB |
| 21,252,906 | 1.3 GiB | 333.9 MiB | 14.0s | 64.1 MiB |

The dump grows about 500 times from the first row to the last and memory about 2.7 times, so it streams. The first large run took 18.7s. The restore drill on the large database took 112.9s, of which 79.6s is `pg_restore` rebuilding 15 million rows and their indexes. Compression is about 4x on this repetitive made-up text. The large database is `toko_maju` from `demodata` (1,250,000 customers, 2,000 products, 5,000,000 orders, 15,000,906 order items).

## Notes

- I've only run it for real against MinIO and Postgres 17. The AWS S3, R2 and B2 paths use the same client and are not tested against those services. Telegram alerts are tested against a local mock server only.
- Metrics live in memory. On start the backup gauges come from the newest manifest in the bucket, but the drill gauges are absent until the first drill and the counters start at 0. There is no auth on `/metrics`; it listens on loopback by default.
- The drill needs a server where it can `CREATE DATABASE` and disk for a full copy. It uses the source server unless `verify.scratch_url` is set, and the compose example drills on the same one. That is the slow part, as the 79.6s restore shows. One database per config file. A 16 MiB part size caps an upload at about 156 GiB stored (10,000 parts). Backup ids have one-second resolution, so two runs in the same second race: one is refused, but only if the other has finished its manifest.
- No WAL archiving, point-in-time recovery or physical backups, so you can lose everything since the last dump. Lose the age private key and the backups are unreadable. Backup times above come from a laptop that was busy with other jobs.
- Next: per-table sampling checks. Parallel `pg_restore` jobs would mean spooling the decrypted dump to scratch disk, because `-j` needs a seekable file and cannot read stdin.

## License

MIT
