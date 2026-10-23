package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"filippo.io/age"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/pg"
	"github.com/ramaraihan12/pg-backup/internal/pipeline"
)

// Backuper takes one backup.
type Backuper struct {
	Catalog     Catalog
	Tools       pg.Tools
	Recipients  []age.Recipient
	DatabaseURL string
	// Now is replaceable in tests.
	Now func() time.Time
	Log *slog.Logger
}

// Run streams pg_dump through zstd and age into the bucket, then writes the
// manifest. The dump is never written to local disk.
//
//	pg_dump stdout -> sha256 -> zstd -> age -> sha256 -> multipart upload
//
// The manifest is written last, so a backup that has a manifest is complete.
func (b *Backuper) Run(ctx context.Context) (*manifest.Manifest, error) {
	now := b.Now
	if now == nil {
		now = time.Now
	}
	log := b.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := b.Catalog
	started := now()
	id := manifest.NewID(started)
	objectKey := manifest.ObjectKey(c.Prefix, c.Database, id)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	enc, err := pipeline.NewEncoder(pw, b.Recipients...)
	if err != nil {
		return nil, err
	}

	uploadDone := make(chan error, 1)
	go func() {
		err := c.Store.Put(ctx, objectKey, pr)
		// Unblock the writer if the upload died early.
		pr.CloseWithError(errOr(err, io.ErrClosedPipe))
		if err != nil {
			cancel()
		}
		uploadDone <- err
	}()

	log.Info("backup started", "database", c.Database, "id", id)
	info, dumpErr := b.Tools.Dump(ctx, b.DatabaseURL, enc)
	if dumpErr == nil {
		dumpErr = enc.Close()
	}
	if dumpErr != nil {
		pw.CloseWithError(dumpErr)
	} else {
		pw.Close()
	}
	uploadErr := <-uploadDone

	if uploadErr != nil || dumpErr != nil {
		// Report the upload error first: when the upload dies, the dump error
		// is only a broken pipe.
		cause := uploadErr
		if cause == nil {
			cause = dumpErr
		}
		// A failed multipart upload is aborted by the S3 client, so there is
		// normally nothing to clean up. Delete anyway in case a short
		// object was committed.
		_ = c.Store.Delete(context.WithoutCancel(ctx), objectKey)
		return nil, fmt.Errorf("backup failed: %w", cause)
	}

	finished := now()
	stats := enc.Stats()
	m := &manifest.Manifest{
		Version:         manifest.SchemaVersion,
		ID:              id,
		Database:        c.Database,
		ServerVersion:   info.ServerVersion,
		PgDumpVersion:   info.PgDumpVersion,
		StartedAt:       started.UTC(),
		FinishedAt:      finished.UTC(),
		DurationSeconds: finished.Sub(started).Seconds(),
		Object:          objectKey,
		RawBytes:        stats.RawBytes,
		StoredBytes:     stats.StoredBytes,
		RawSHA256:       stats.RawSHA256,
		Checksum:        stats.StoredSHA256,
		Tables:          info.Tables,
	}
	data, err := m.Marshal()
	if err != nil {
		return nil, err
	}
	// Write the manifest even if ctx was cancelled meanwhile: the object is
	// already uploaded, and a manifest-less object is wasted.
	if err := c.Store.Put(context.WithoutCancel(ctx), manifest.ManifestKey(c.Prefix, c.Database, id), bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	log.Info("backup finished", "id", id, "raw_bytes", m.RawBytes, "stored_bytes", m.StoredBytes, "seconds", m.DurationSeconds)
	return m, nil
}

func errOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
