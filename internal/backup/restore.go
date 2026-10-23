package backup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/pg"
	"github.com/ramaraihan12/pg-backup/internal/pipeline"
)

// Restorer restores backups into a database.
type Restorer struct {
	Catalog    Catalog
	Tools      pg.Tools
	Identities []age.Identity
}

// Restore downloads, decrypts and decompresses a backup straight into
// pg_restore. The target database must exist.
//
// After pg_restore finishes, the rest of the stream is read and the stored
// bytes are checked against the manifest checksum. If anything failed, the
// checksum is checked as well, so the error says "checksum mismatch" when the
// object was damaged, instead of whatever pg_restore made of the garbage.
func (r *Restorer) Restore(ctx context.Context, m *manifest.Manifest, targetDSN string, opts pg.RestoreOptions) error {
	body, err := r.Catalog.Store.Get(ctx, m.Object)
	if err != nil {
		return fmt.Errorf("download backup: %w", err)
	}
	defer body.Close()

	dec, err := pipeline.NewDecoder(body, r.Identities...)
	defer dec.Close()
	if err != nil {
		return explain(dec, m, err)
	}

	restoreErr := r.Tools.Restore(ctx, targetDSN, dec, opts)
	if restoreErr == nil {
		// pg_restore may stop reading before the end of the stream. Finish it
		// so the final age chunk is authenticated.
		if _, err := io.Copy(io.Discard, dec); err != nil {
			restoreErr = err
		}
	}
	if restoreErr != nil {
		return explain(dec, m, restoreErr)
	}
	if err := dec.VerifyStored(m.Checksum); err != nil {
		return err
	}
	if dec.RawBytes() != m.RawBytes {
		return fmt.Errorf("restored %d bytes but the manifest says the dump had %d", dec.RawBytes(), m.RawBytes)
	}
	return nil
}

// explain turns a failure into a checksum error when the stored bytes were damaged.
func explain(dec *pipeline.Decoder, m *manifest.Manifest, cause error) error {
	if err := dec.VerifyStored(m.Checksum); errors.Is(err, pipeline.ErrChecksum) {
		return fmt.Errorf("%w (restore stopped with: %v)", err, cause)
	}
	return cause
}
