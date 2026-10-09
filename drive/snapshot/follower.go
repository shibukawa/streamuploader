package snapshot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"streamuploader/drive/meta"
	"streamuploader/drive/objerr"
	"streamuploader/drive/sidecar"
)

// Follower is the server's side of the snapshot: it keeps a read-only
// sidecar on a local copy of the published index and the meta cache in step
// with the pointer. It stands in for the indexer in server mode: the journal
// overlay starts after the published last_journal_key.
type Follower struct {
	Store  *Store
	Search *sidecar.Client
	Metas  *meta.Cache
	Dir    string
	Logger *slog.Logger

	mu          sync.Mutex
	pointer     *Pointer
	etag        string
	refreshedAt time.Time
	lastError   string
	refreshes   int64
}

func NewFollower(store *Store, search *sidecar.Client, metas *meta.Cache, dir string, logger *slog.Logger) *Follower {
	if logger == nil {
		logger = slog.Default()
	}
	return &Follower{Store: store, Search: search, Metas: metas, Dir: dir, Logger: logger}
}

// LastJournalKey is the published pointer's; everything after it is overlaid
// from the journal by the server.
func (f *Follower) LastJournalKey() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pointer == nil {
		return ""
	}
	return f.pointer.LastJournalKey
}

// Poke is a no-op: the indexer runs elsewhere and folds on its own cadence.
func (f *Follower) Poke() {}

// Status describes the followed snapshot for /api/drive/stats.
func (f *Follower) Status() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{
		"mode":       "server",
		"generation": int64(0),
		"refreshes":  f.refreshes,
	}
	if f.pointer != nil {
		out["generation"] = f.pointer.Generation
		out["published_at"] = f.pointer.PublishedAt
		out["last_journal_key"] = f.pointer.LastJournalKey
		out["num_docs"] = f.pointer.NumDocs
		out["files"] = len(f.pointer.Files)
	}
	if !f.refreshedAt.IsZero() {
		out["refreshed_at"] = f.refreshedAt
	}
	if f.lastError != "" {
		out["last_error"] = f.lastError
	}
	return out
}

// Generation is the generation currently served, 0 before the first load.
func (f *Follower) Generation() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pointer == nil {
		return 0
	}
	return f.pointer.Generation
}

// Start launches the read-only sidecar and loads the current snapshot. A
// missing pointer is not an error: the server serves an empty index plus
// the journal overlay until the indexer publishes.
func (f *Follower) Start(ctx context.Context) error {
	f.Search.ReadOnly = true
	if err := f.Search.Start(ctx); err != nil {
		if !errors.Is(err, sidecar.ErrSchemaMismatch) {
			return err
		}
		f.Logger.Warn("index_dir_schema_mismatch_clearing", "dir", f.Dir, "error", err)
		if err := f.Search.Reset(ctx); err != nil {
			return err
		}
	}
	if _, err := f.Refresh(ctx); err != nil {
		return err
	}
	return nil
}

// Refresh compares the pointer with what is served and, when it changed,
// downloads the new files, reloads the sidecar and updates the meta cache.
// It reports whether a new generation was loaded.
func (f *Follower) Refresh(ctx context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	changed, err := f.refreshLocked(ctx)
	f.refreshedAt = time.Now().UTC()
	if err != nil {
		f.lastError = err.Error()
	} else {
		f.lastError = ""
	}
	return changed, err
}

func (f *Follower) refreshLocked(ctx context.Context) (bool, error) {
	etag, found, err := f.Store.HeadPointer(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		if f.pointer != nil {
			f.Logger.Warn("index_snapshot_pointer_missing", "serving_generation", f.pointer.Generation)
		}
		return false, nil
	}
	if f.pointer != nil && etag == f.etag {
		return false, nil
	}
	p, etag, found, err := f.Store.ReadPointer(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if want := f.Search.SchemaVersion(); want != "" && p.SchemaVersion != want {
		return false, fmt.Errorf("snapshot: pointer has schema %q, this sidecar expects %q; waiting for the indexer to republish", p.SchemaVersion, want)
	}
	fetched, err := f.Store.Download(ctx, p, f.Dir)
	if err != nil {
		return false, err
	}
	numDocs, err := f.Search.Reload(ctx)
	if err != nil {
		return false, fmt.Errorf("snapshot: reload sidecar: %w", err)
	}
	if err := f.refreshMetas(ctx, p); err != nil {
		return false, err
	}
	prev := f.pointer
	f.pointer = p
	f.etag = etag
	f.refreshes++
	pruned, err := Prune(f.Dir, p.Files)
	if err != nil {
		f.Logger.Warn("index_snapshot_prune_failed", "error", err)
	}
	prevGen := int64(0)
	if prev != nil {
		prevGen = prev.Generation
	}
	f.Logger.Info("index_snapshot_loaded", "generation", p.Generation, "from_generation", prevGen, "fetched", fetched, "pruned", pruned, "num_docs", numDocs, "files_cached", f.Metas.Len())
	return true, nil
}

// refreshMetas brings the meta cache to the state of pointer p: only the
// files the history says changed, or everything when the history does not
// reach back to the served generation.
func (f *Follower) refreshMetas(ctx context.Context, p *Pointer) error {
	var ids []string
	full := true
	if f.pointer != nil {
		var ok bool
		ids, full, ok = p.ChangesSince(f.pointer.Generation)
		if !ok {
			full = true
		}
	}
	if full {
		fresh := meta.NewCache()
		if _, err := fresh.LoadSnapshots(ctx, f.Store.Objects, f.Store.Bucket, f.Store.Prefix, f.Store.Tenant); err != nil {
			return fmt.Errorf("snapshot: load meta snapshots: %w", err)
		}
		f.Metas.ReplaceAll(fresh.All())
		return nil
	}
	for _, id := range ids {
		file, err := meta.ReadSnapshot(ctx, f.Store.Objects, f.Store.Bucket, meta.SnapshotKey(f.Store.Prefix, f.Store.Tenant, id))
		if err != nil {
			if objerr.IsNotFound(err) {
				f.Metas.Delete(id)
				continue
			}
			return fmt.Errorf("snapshot: read meta %s: %w", id, err)
		}
		f.Metas.Put(file)
	}
	return nil
}

// RunLoop refreshes every interval until ctx ends.
func (f *Follower) RunLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := f.Refresh(ctx); err != nil {
				f.Logger.Error("index_snapshot_refresh_failed", "error", err)
			}
		}
	}
}
