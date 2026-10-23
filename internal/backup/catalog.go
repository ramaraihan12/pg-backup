// Package backup holds the operations on a backup set: take one, list them,
// restore one, prune old ones.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
	"github.com/ramaraihan12/pg-backup/internal/retention"
	"github.com/ramaraihan12/pg-backup/internal/store"
)

// Catalog is the set of backups of one database in one bucket.
type Catalog struct {
	Store    store.Store
	Prefix   string
	Database string
}

// Listing is the result of reading the bucket.
type Listing struct {
	// Backups is sorted newest first.
	Backups []*manifest.Manifest
	// Problems are things worth showing the operator: unreadable manifests,
	// manifests whose object is gone.
	Problems []string
}

// ErrNoBackups is returned when there is nothing to pick from.
var ErrNoBackups = errors.New("no backups found")

// List reads every manifest under the database's prefix.
func (c Catalog) List(ctx context.Context) (*Listing, error) {
	objs, err := c.Store.List(ctx, manifest.Dir(c.Prefix, c.Database))
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, o := range objs {
		present[o.Key] = true
	}
	l := &Listing{}
	for _, o := range objs {
		id, ok := manifest.IDFromManifestKey(c.Prefix, c.Database, o.Key)
		if !ok {
			continue
		}
		m, err := c.readManifest(ctx, o.Key)
		if err != nil {
			l.Problems = append(l.Problems, fmt.Sprintf("%s: %v", o.Key, err))
			continue
		}
		// Prune deletes m.Object, so refuse a manifest that points anywhere
		// other than the key this backup should have.
		if m.ID != id || m.Database != c.Database || m.Object != manifest.ObjectKey(c.Prefix, c.Database, id) {
			l.Problems = append(l.Problems, fmt.Sprintf("%s: manifest does not match its own key, ignored", o.Key))
			continue
		}
		if !present[m.Object] {
			l.Problems = append(l.Problems, fmt.Sprintf("%s: the dump object %s is missing", o.Key, m.Object))
		}
		l.Backups = append(l.Backups, m)
	}
	sort.Slice(l.Backups, func(i, j int) bool { return l.Backups[i].ID > l.Backups[j].ID })
	return l, nil
}

func (c Catalog) readManifest(ctx context.Context, key string) (*manifest.Manifest, error) {
	rc, err := c.Store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
	if err != nil {
		return nil, err
	}
	return manifest.Parse(data)
}

// Resolve finds one backup. An empty id or "latest" means the newest.
func (c Catalog) Resolve(ctx context.Context, id string) (*manifest.Manifest, error) {
	l, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(l.Backups) == 0 {
		return nil, fmt.Errorf("%w for database %q under %q", ErrNoBackups, c.Database, c.Prefix)
	}
	if id == "" || id == "latest" {
		return l.Backups[0], nil
	}
	for _, m := range l.Backups {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("no backup with id %q (run list to see the ids)", id)
}

// Delete removes a backup. The manifest goes first: once it is gone the
// backup no longer exists as far as list and restore are concerned, so a
// crash between the two deletes leaves harmless garbage and not a manifest
// that points at nothing.
func (c Catalog) Delete(ctx context.Context, m *manifest.Manifest) error {
	if err := c.Store.Delete(ctx, manifest.ManifestKey(c.Prefix, c.Database, m.ID)); err != nil {
		return err
	}
	return c.Store.Delete(ctx, m.Object)
}

// PruneEntry is the outcome for one backup.
type PruneEntry struct {
	Backup  *manifest.Manifest
	Keep    bool
	Reasons []string
	Deleted bool
	Err     error
}

// Prune applies a retention policy. With apply false nothing is deleted.
func (c Catalog) Prune(ctx context.Context, p retention.Policy, apply bool) ([]PruneEntry, error) {
	l, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]retention.Item, len(l.Backups))
	byID := map[string]*manifest.Manifest{}
	for i, m := range l.Backups {
		items[i] = retention.Item{ID: m.ID, Time: m.Time()}
		byID[m.ID] = m
	}
	decisions, err := retention.Plan(items, p)
	if err != nil {
		return nil, err
	}
	out := make([]PruneEntry, 0, len(decisions))
	for _, d := range decisions {
		e := PruneEntry{Backup: byID[d.ID], Keep: d.Keep, Reasons: d.Reasons}
		if apply && !d.Keep {
			if err := c.Delete(ctx, e.Backup); err != nil {
				e.Err = err
			} else {
				e.Deleted = true
			}
		}
		out = append(out, e)
	}
	return out, nil
}
