# pg-backup

[![ci](https://github.com/ramaraihan12/pg-backup/actions/workflows/ci.yml/badge.svg)](https://github.com/ramaraihan12/pg-backup/actions/workflows/ci.yml)

A small Go tool that streams `pg_dump` output through zstd and age into an S3-compatible bucket, keeps a grandfather-father-son retention policy, and proves each backup by restoring it into a scratch database. A backup you haven't restored is not a backup, so the restore drill is the main feature. It runs against MinIO by default, so trying it costs nothing.

All demo data is made up: an Indonesian shop called `toko_maju` with customers, products, orders and order items, produced by a generator in this repo.

## What it looks like

Real runs on my laptop (Ryzen 9 5900HX, 16 GB, Docker Desktop) against Postgres 17.11 and a local MinIO, both in containers. The database is 21 million made-up rows, 2.7 GB on disk.

```console
$ pg-backup backup
backup 20261024T010804Z  demo_large (postgres 17.11)
  raw        1.3 GiB
  stored     339.6 MiB  (4.0x smaller, zstd + age)
  rows       21,252,906 in 4 tables
  took       11.1s
  peak rss   64.3 MiB
  checksum   2e8efc7b5bed5650...
  object     pg-backup/demo_large/demo_large-20261024T010804Z.dump.zst.age

$ pg-backup list
ID                TAKEN (UTC)       STORED     RAW      ROWS        TOOK   SERVER
20261024T010804Z  2026-10-24 01:08  339.6 MiB  1.3 GiB  21,252,906  11.1s  17.11

$ pg-backup verify
verify 20261024T010804Z  demo_large, 339.6 MiB stored, 21,252,906 rows expected
  scratch database  pgbackup_verify_20261024t010804z_95f73d
  restore           ok    68.4s
  row counts        ok    1.6s
    public.customers            1,250,000  ok
    public.order_items         15,000,906  ok
    public.orders               5,000,000  ok
    public.products                 2,000  ok
  check "every order has a customer"             ok
  check "order totals match their items"         ok
  cleanup           dropped  1.4s
PASS in 84.7s
```

Then I flipped one byte in the middle of a stored object (a 6.6 MiB backup of a smaller database) and ran verify again. It stops at the checksum and still drops the scratch database:

```console
$ pg-backup verify
verify 20261024T011001Z  demo_medium, 6.6 MiB stored, 426,813 rows expected
  scratch database  pgbackup_verify_20261024t011001z_5c3623
  restore           FAIL  158ms
  cleanup           dropped  154ms
FAIL in 673ms
  - restore: checksum mismatch: stored sha256 is 4e99f43d..., manifest says 0bdc813d... (restore stopped with: pg_restore failed: exit status 1: pg_restore: error: could not read from input file: end of file)
error: restore drill failed
```

`prune` shows what it would do and deletes nothing without `--yes`. This bucket holds 73 backups that I faked by copying one real backup under older ids (about daily for 40 days, then every 5 days), with a policy of 7 daily, 4 weekly, 6 monthly (trimmed):

```console
$ pg-backup prune
dry run, nothing is deleted (add --yes to delete): keep 7 daily, 4 weekly, 6 monthly
  keep    20261024T011025Z  newest, daily 2026-10-24, weekly 2026-W43, monthly 2026-10
  keep    20261023T030000Z  daily 2026-10-23
  (trimmed)
  keep    20261018T030000Z  daily 2026-10-18, weekly 2026-W42
  delete  20261017T030000Z  694.5 KiB
  (trimmed)
  keep    20261011T030000Z  weekly 2026-W41
  keep    20260930T030000Z  monthly 2026-09
  keep    20260527T030000Z  monthly 2026-05
  delete  20260522T030000Z  694.5 KiB
  (trimmed)
14 kept, 59 would be deleted (40.0 MiB)
```

## How it works

- `backup` runs `pg_dump` in custom format and pipes stdout through zstd, then age, then a multipart upload (16 MiB parts). There is no temp file. SHA-256 is computed at both ends of the stream, over the raw dump and over the stored bytes.
- Row counts in the manifest are exact. The tool opens one repeatable-read transaction, exports its snapshot, starts `pg_dump --snapshot` with it and counts every table inside that same transaction, so the counts match the dump even while writes continue.
- The manifest JSON is uploaded last, next to the object. A backup without a manifest does not exist as far as `list` and `prune` are concerned, so a failed upload leaves nothing that looks valid.
- `verify` downloads, decrypts, decompresses and `pg_restore`s into a fresh `pgbackup_verify_*` database, compares row counts with the manifest, runs the read-only SQL checks from the config (each must return one boolean), then drops the scratch database, even on failure.
- `prune` keeps the newest backup of each of the last N days, ISO weeks and months. A backup is kept if any rule keeps it, and the newest one is always kept. It is a dry run unless you pass `--yes`.
- `serve` runs backup, then verify, then optionally prune on a cron schedule, and exposes `/metrics` (last success time, duration, raw and stored size, verify result and time). A failure can notify a Telegram bot, off by default.

## Numbers

Three made-up databases from the same generator, one backup each, same laptop. Peak RSS is `VmHWM` of the pg-backup process, read when it exits.

| Database rows | Raw dump | Stored (zstd + age) | Backup time | Peak RSS |
|---|---|---|---|---|
| 44,567 | 2.6 MiB | 694.5 KiB | 0.2s | 24.9 MiB |
| 426,813 | 25.6 MiB | 6.6 MiB | 0.5s | 36.6 MiB |
| 21,252,906 | 1.3 GiB | 339.6 MiB | 11.1s | 64.3 MiB |

The dump grows about 500 times from the first row to the last and memory grows about 2.6 times, so it is streaming. A first run of the large backup on a cold cache took 28.5s and peaked at 53 MiB. The restore drill on the large database took 84.7s, of which 68.4s is `pg_restore` rebuilding 15 million rows and their indexes. Compression is about 4x on this data, which is repetitive made-up text, so real data will differ. Raw logs are not in the repo.

The large database is `toko_maju` generated with `demodata`: 1,250,000 customers, 2,000 products, 5,000,000 orders and 15,000,906 order items.

## Stack

Go 1.27.3, cobra 1.10, klauspost/compress 1.20 (zstd), age 1.3 (filippo.io/age), minio-go 7.4, pgx 5.11, robfig/cron 3, prometheus client_golang 1.12, testcontainers-go 0.44, staticcheck, PostgreSQL 17 client tools and MinIO.

## Run it

You need Docker. Go is only needed if you want to build outside the container.

```bash
cp .env.example .env
docker run --rm $(docker build -q .) keygen     # paste both keys into .env
docker compose up -d
docker compose --profile demo run --rm seed     # optional: 20,000 made-up customers, 80,000 orders
docker compose exec pg-backup pg-backup backup
docker compose exec pg-backup pg-backup list
docker compose exec pg-backup pg-backup verify
docker compose exec pg-backup pg-backup prune
curl -s localhost:9187/metrics | grep ^pg_backup
```

Compose publishes Postgres (5442), MinIO (9010, console 9011) and the metrics port (9187) on `127.0.0.1` only. The `pg-backup` service runs `serve` with the schedule in `config.example.yaml` (03:00 daily). Add `--run-now` to also run once at start.

Without Docker, build with `go build ./cmd/pg-backup`, copy `config.example.yaml` to `pg-backup.yaml` and have `pg_dump` and `pg_restore` 17 on `PATH` (or set `postgres.bin_dir`). `restore --to <url> [--id <id>]` restores into an existing, empty database.

Secrets only come from the environment. A `password:` key in the YAML is an error. Any setting can be overridden with an env var:

| Variable | Default | |
|---|---|---|
| `PGBACKUP_DATABASE_URL` | from YAML | source database, password via `PGBACKUP_DB_PASSWORD` |
| `PGBACKUP_S3_ENDPOINT`, `_BUCKET`, `_PREFIX` | from YAML | any S3-compatible store |
| `PGBACKUP_S3_ACCESS_KEY`, `_SECRET_KEY` | empty | required |
| `PGBACKUP_AGE_RECIPIENT` | empty | public key, needed to back up |
| `PGBACKUP_AGE_IDENTITY` or `_FILE` | empty | private key, needed to restore and verify |
| `PGBACKUP_RETENTION_DAILY`, `_WEEKLY`, `_MONTHLY` | 7, 4, 6 | |
| `PGBACKUP_SCHEDULE` | `0 3 * * *` | 5-field cron |
| `PGBACKUP_TELEGRAM`, `_TELEGRAM_TOKEN`, `_TELEGRAM_CHAT_ID` | off | failure alerts |

## Tests

```bash
go test -short ./...        # unit tests, no Docker
go test ./...               # adds the integration suite
```

The unit tests cover the retention rules (table-driven), the manifest, config loading, the metrics and the Telegram notifier against a local HTTP server, and the stream pipeline: compress and encrypt, then decrypt and decompress, gives identical bytes, and a flipped byte is reported as a checksum mismatch. The integration suite starts Postgres 17 and MinIO with testcontainers and checks backup, list and verify, a corrupted object, a missing object, a tampered manifest, a failing SQL check, restore with the wrong key, a failed backup leaving nothing behind, and prune removing the right objects. `-short` skips it. CI runs gofmt, `go vet`, staticcheck, the unit tests with `-race`, the integration suite, and a Docker build. The integration job installs Postgres 17 client tools because the runner ships 16 and `pg_dump` refuses a newer server.

## Notes

- I've only run it for real against MinIO and Postgres 17. The AWS S3, R2 and B2 paths use the same client and are not tested against those services.
- Telegram alerts are tested against a local mock server, never against the real Bot API.
- One database per config file. Run more than one instance for more databases.
- The drill needs a server where it can `CREATE DATABASE`, and disk for a full copy of the data. On a big database that is the slow part, as the 68s restore above shows.
- No WAL archiving or point-in-time recovery, no physical backups. You can lose everything since the last dump.
- If you lose the age private key, the backups are unreadable. There is no recovery path.
- The `prune` demo uses copies of one backup under older ids, so it shows the selection rules and not real aging data.
- Next: parallel `pg_restore` jobs for the drill, and per-table sampling checks.

## License

MIT
