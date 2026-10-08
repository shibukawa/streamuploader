// Package indexer is the Drive's single writer: it folds journal events and
// extracted text into the tantivy index through the sidecar, keeps the meta
// cache and snapshots current, rebuilds everything from the bucket, and
// publishes the index to the bucket as a snapshot that servers follow.
// See .knowledge/concepts/system/drive-indexer.yaml.
package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"streamuploader/drive/journal"
	"streamuploader/drive/meta"
	"streamuploader/drive/objerr"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/snapshot"
	"streamuploader/internal/extraction"
	"streamuploader/internal/storage"
)

const stateFileName = "drive-state.json"

type Config struct {
	Tenant string
	Bucket string
	Prefix string
	// IndexDir is the sidecar's directory; the indexer state lives next to
	// the tantivy files so wiping the directory forces a rebuild.
	IndexDir        string
	Limits          sidecar.Limits
	MaxEventsPerRun int
	// MaxTextBytes bounds one .text.json read.
	MaxTextBytes int64
	// Snapshot syncs the index with search/{tenant}/ in the bucket: at
	// start a published generation the local directory does not have is
	// adopted instead of rebuilt, and every commit is published so that
	// servers (drive server) and the next indexer process can follow it.
	Snapshot bool
	// GCGrace is how long a segment file no pointer references stays in the
	// bucket; longer than any server's refresh interval plus download time.
	GCGrace time.Duration
	// SnapshotNow replaces the snapshot clock in tests.
	SnapshotNow func() time.Time
}

// State is what the indexer has folded so far.
type State struct {
	LastJournalKey string            `json:"last_journal_key"`
	PendingText    map[string]string `json:"pending_text"`
	SchemaVersion  string            `json:"schema_version"`
	// Generation is the snapshot generation this directory was last
	// published as or adopted from; 0 when the index was never synced.
	Generation int64     `json:"generation,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Report summarizes one run.
type Report struct {
	Events       int
	FilesIndexed int
	FilesDeleted int
	PendingText  int
	Rebuilt      bool
	// Adopted is set when Start replaced the local index with the
	// published snapshot instead of rebuilding.
	Adopted bool
	// Published is set when the run wrote a new snapshot generation.
	Published  bool
	Generation int64
	Duration   time.Duration
}

type Indexer struct {
	cfg     Config
	store   storage.Store
	journal *journal.Store
	search  *sidecar.Client
	metas   *meta.Cache
	logger  *slog.Logger
	snap    *snapshot.Store

	runMu   sync.Mutex
	stateMu sync.RWMutex
	state   State
	poke    chan struct{}
	rebuild chan struct{}

	// Snapshot bookkeeping. pointer is what this process last published
	// or adopted; the unpublished fields describe local commits the bucket
	// does not have yet (guarded by stateMu, changed only under runMu).
	pointer     *snapshot.Pointer
	pointerETag string
	unpublished bool
	unpubIDs    []string
	unpubFull   bool
	publishErr  string
}

func New(cfg Config, store storage.Store, jstore *journal.Store, search *sidecar.Client, metas *meta.Cache, logger *slog.Logger) *Indexer {
	if cfg.MaxEventsPerRun <= 0 {
		cfg.MaxEventsPerRun = 1000
	}
	if cfg.MaxTextBytes <= 0 {
		cfg.MaxTextBytes = 64 << 20
	}
	if cfg.Limits.MaxBodyBytes <= 0 {
		cfg.Limits = sidecar.DefaultLimits
	}
	if cfg.GCGrace <= 0 {
		cfg.GCGrace = 15 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	ix := &Indexer{
		cfg:     cfg,
		store:   store,
		journal: jstore,
		search:  search,
		metas:   metas,
		logger:  logger,
		state:   State{PendingText: map[string]string{}},
		poke:    make(chan struct{}, 1),
		rebuild: make(chan struct{}, 1),
	}
	if cfg.Snapshot {
		ix.snap = &snapshot.Store{Objects: store, Bucket: cfg.Bucket, Prefix: cfg.Prefix, Tenant: cfg.Tenant, Logger: logger, Now: cfg.SnapshotNow}
	}
	return ix
}

// LastJournalKey is the newest journal key folded into the index; the server
// overlays everything after it.
func (ix *Indexer) LastJournalKey() string {
	ix.stateMu.RLock()
	defer ix.stateMu.RUnlock()
	return ix.state.LastJournalKey
}

// Poke asks the loop to run soon.
func (ix *Indexer) Poke() {
	select {
	case ix.poke <- struct{}{}:
	default:
	}
}

// RebuildAsync asks the loop to rebuild from the bucket.
func (ix *Indexer) RebuildAsync() {
	select {
	case ix.rebuild <- struct{}{}:
	default:
	}
}

// Status describes the indexer for /api/drive/stats.
func (ix *Indexer) Status() map[string]any {
	ix.stateMu.RLock()
	defer ix.stateMu.RUnlock()
	out := map[string]any{
		"mode":             "indexer",
		"snapshot":         ix.snap != nil,
		"generation":       ix.state.Generation,
		"last_journal_key": ix.state.LastJournalKey,
		"pending_text":     len(ix.state.PendingText),
		"unpublished":      ix.unpublished,
	}
	if ix.publishErr != "" {
		out["last_publish_error"] = ix.publishErr
	}
	return out
}

// Generation is the snapshot generation of the local index, 0 when never synced.
func (ix *Indexer) Generation() int64 {
	ix.stateMu.RLock()
	defer ix.stateMu.RUnlock()
	return ix.state.Generation
}

func (ix *Indexer) statePath() string {
	return filepath.Join(ix.cfg.IndexDir, stateFileName)
}

func (ix *Indexer) loadState() (bool, error) {
	body, err := os.ReadFile(ix.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return false, err
	}
	if st.PendingText == nil {
		st.PendingText = map[string]string{}
	}
	ix.stateMu.Lock()
	ix.state = st
	ix.stateMu.Unlock()
	return true, nil
}

func (ix *Indexer) saveState() error {
	ix.stateMu.RLock()
	st := ix.state
	ix.stateMu.RUnlock()
	st.UpdatedAt = time.Now().UTC()
	st.SchemaVersion = ix.search.SchemaVersion()
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := ix.statePath() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ix.statePath())
}

// Start opens the sidecar and makes the index consistent with the bucket.
// With snapshots on, a published generation the local directory does not
// match is adopted; otherwise a missing state file or a schema mismatch
// triggers a full rebuild. Then the journal is folded up to now and the
// result is published.
func (ix *Indexer) Start(ctx context.Context) (Report, error) {
	ix.runMu.Lock()
	defer ix.runMu.Unlock()
	rep := Report{}
	localOK := true
	if err := ix.search.Start(ctx); err != nil {
		if !errors.Is(err, sidecar.ErrSchemaMismatch) {
			return rep, err
		}
		ix.logger.Warn("index_schema_mismatch_clearing", "error", err)
		if err := ix.search.Reset(ctx); err != nil {
			return rep, fmt.Errorf("reset index: %w", err)
		}
		localOK = false
	}
	if localOK {
		found, err := ix.loadState()
		if err != nil {
			ix.logger.Warn("index_state_unreadable", "error", err)
			localOK = false
		} else if !found {
			localOK = false
		}
	}
	var remote *snapshot.Pointer
	remoteETag := ""
	if ix.snap != nil {
		p, etag, found, err := ix.snap.ReadPointer(ctx)
		if err != nil {
			return rep, err
		}
		if found {
			if want := ix.search.SchemaVersion(); p.SchemaVersion != want {
				ix.logger.Warn("index_snapshot_schema_differs", "published", p.SchemaVersion, "sidecar", want, "note", "the published snapshot is ignored and will be replaced")
			} else {
				remote, remoteETag = p, etag
			}
		}
	}
	if remote != nil {
		adopt := !localOK
		if localOK && ix.Generation() != remote.Generation {
			ix.logger.Info("index_snapshot_generation_differs", "local", ix.Generation(), "published", remote.Generation)
			adopt = true
		}
		if adopt {
			if err := ix.adoptLocked(ctx, remote, remoteETag); err != nil {
				return rep, fmt.Errorf("adopt index snapshot: %w", err)
			}
			rep.Adopted = true
			localOK = true
		}
	}
	if !localOK {
		return ix.rebuildLocked(ctx)
	}
	if ix.metas.Len() == 0 {
		if _, err := ix.metas.LoadSnapshots(ctx, ix.store, ix.cfg.Bucket, ix.cfg.Prefix, ix.cfg.Tenant); err != nil {
			return Report{}, fmt.Errorf("load snapshots: %w", err)
		}
	}
	// An index that holds documents for a bucket without snapshots belongs
	// to another bucket or an earlier life of this one; start over.
	if ix.metas.Len() == 0 {
		if st, err := ix.search.Stats(ctx); err == nil && st.NumDocs > 0 {
			ix.logger.Warn("index_has_docs_but_bucket_has_no_snapshots_rebuilding", "num_docs", st.NumDocs)
			return ix.rebuildLocked(ctx)
		}
	}
	if ix.snap != nil && !rep.Adopted {
		// Local commits the bucket does not have: a crash between commit
		// and publish, a deleted pointer, or snapshots newly switched on.
		st, err := ix.search.Stats(ctx)
		if err != nil {
			return rep, err
		}
		ix.pointer, ix.pointerETag = remote, remoteETag
		if remote == nil || remote.Opstamp != st.Opstamp {
			ix.markUnpublished(nil, true)
		}
	}
	tail, err := ix.runOnceLocked(ctx)
	rep.Events = tail.Events
	rep.FilesIndexed = tail.FilesIndexed
	rep.FilesDeleted = tail.FilesDeleted
	rep.PendingText = tail.PendingText
	rep.Published = tail.Published
	rep.Generation = tail.Generation
	rep.Duration = tail.Duration
	return rep, err
}

// adoptLocked replaces the local index with the published snapshot: the
// sidecar is stopped while files and meta.json are swapped, then the state
// and the meta cache are taken from the pointer and the bucket.
func (ix *Indexer) adoptLocked(ctx context.Context, p *snapshot.Pointer, etag string) error {
	ix.logger.Info("index_snapshot_adopting", "generation", p.Generation, "files", len(p.Files), "num_docs", p.NumDocs)
	ix.search.Stop()
	fetched, err := ix.snap.Download(ctx, p, ix.cfg.IndexDir)
	if err != nil {
		return err
	}
	if _, err := snapshot.Prune(ix.cfg.IndexDir, p.Files); err != nil {
		return err
	}
	if err := ix.search.Start(ctx); err != nil {
		return err
	}
	pending := make(map[string]string, len(p.PendingText))
	for k, v := range p.PendingText {
		pending[k] = v
	}
	ix.stateMu.Lock()
	ix.state = State{LastJournalKey: p.LastJournalKey, PendingText: pending, Generation: p.Generation}
	ix.unpublished, ix.unpubIDs, ix.unpubFull, ix.publishErr = false, nil, false, ""
	ix.stateMu.Unlock()
	if err := ix.saveState(); err != nil {
		return err
	}
	fresh := meta.NewCache()
	if _, err := fresh.LoadSnapshots(ctx, ix.store, ix.cfg.Bucket, ix.cfg.Prefix, ix.cfg.Tenant); err != nil {
		return fmt.Errorf("load snapshots: %w", err)
	}
	ix.metas.ReplaceAll(fresh.All())
	ix.pointer, ix.pointerETag = p, etag
	ix.logger.Info("index_snapshot_adopted", "generation", p.Generation, "fetched", fetched, "files_cached", ix.metas.Len())
	return nil
}

// RunOnce folds new journal events and newly available text.
func (ix *Indexer) RunOnce(ctx context.Context) (Report, error) {
	ix.runMu.Lock()
	defer ix.runMu.Unlock()
	return ix.runOnceLocked(ctx)
}

// Rebuild wipes the index and recreates it from meta snapshots and derived text.
func (ix *Indexer) Rebuild(ctx context.Context) (Report, error) {
	ix.runMu.Lock()
	defer ix.runMu.Unlock()
	return ix.rebuildLocked(ctx)
}

// RunLoop folds on every poke and at least every interval until ctx ends.
func (ix *Indexer) RunLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ix.rebuild:
			if rep, err := ix.Rebuild(ctx); err != nil {
				ix.logger.Error("index_rebuild_failed", "error", err)
			} else {
				ix.logger.Info("index_rebuilt", "files", rep.FilesIndexed, "pending_text", rep.PendingText, "generation", rep.Generation, "duration", rep.Duration)
			}
		case <-ix.poke:
			// Coalesce a burst of pokes into one run shortly after the last.
			time.Sleep(200 * time.Millisecond)
			ix.runAndLog(ctx)
		case <-ticker.C:
			ix.runAndLog(ctx)
		}
	}
}

func (ix *Indexer) runAndLog(ctx context.Context) {
	rep, err := ix.RunOnce(ctx)
	if err != nil {
		ix.logger.Error("index_run_failed", "error", err)
		return
	}
	if rep.Events > 0 || rep.FilesIndexed > 0 || rep.FilesDeleted > 0 || rep.Published {
		ix.logger.Info("index_run", "events", rep.Events, "indexed", rep.FilesIndexed, "deleted", rep.FilesDeleted, "pending_text", rep.PendingText, "published", rep.Published, "generation", rep.Generation, "duration", rep.Duration)
	}
}

func (ix *Indexer) runOnceLocked(ctx context.Context) (Report, error) {
	started := time.Now()
	rep := Report{}
	ix.stateMu.RLock()
	lastKey := ix.state.LastJournalKey
	pending := make(map[string]string, len(ix.state.PendingText))
	for k, v := range ix.state.PendingText {
		pending[k] = v
	}
	ix.stateMu.RUnlock()

	entries, err := ix.journal.ListAfter(ctx, ix.cfg.Tenant, lastKey, ix.cfg.MaxEventsPerRun)
	if err != nil {
		return rep, err
	}
	rep.Events = len(entries)
	changed := map[string]*meta.File{}
	order := []string{}
	for _, e := range entries {
		lastKey = e.Key
		if !e.Event.IsFileState() {
			continue
		}
		st := e.Event.State
		if st.LastEvent == "" {
			st.LastEvent = e.Key
		}
		if _, seen := changed[st.FileID]; !seen {
			order = append(order, st.FileID)
		}
		changed[st.FileID] = st
	}
	// Text that was not ready last time is re-checked for files that have no
	// newer event; a newer event re-reads it anyway.
	pendingIDs := make([]string, 0, len(pending))
	for id := range pending {
		pendingIDs = append(pendingIDs, id)
	}
	sort.Strings(pendingIDs)
	for _, id := range pendingIDs {
		if _, ok := changed[id]; ok {
			continue
		}
		cur, ok := ix.metas.Get(id)
		if !ok || cur.Deleted {
			delete(pending, id)
			continue
		}
		if _, err := ix.store.HeadObject(ctx, storage.HeadInput{Bucket: ix.cfg.Bucket, Key: pending[id]}); err != nil {
			if objerr.IsNotFound(err) {
				continue
			}
			return rep, fmt.Errorf("head text %s: %w", pending[id], err)
		}
		changed[id] = cur.Clone()
		order = append(order, id)
	}
	if len(changed) == 0 {
		if len(entries) > 0 {
			ix.setLast(lastKey, pending)
			if err := ix.saveState(); err != nil {
				return rep, err
			}
			// The pointer's last_journal_key moves too, so servers do not
			// overlay events that carry no file state.
			ix.markUnpublished(nil, false)
		}
		rep.PendingText = len(pending)
		if err := ix.publishPendingLocked(ctx, &rep, nil); err != nil {
			return rep, err
		}
		rep.Duration = time.Since(started)
		return rep, nil
	}

	var docs []sidecar.Doc
	var deleted []string
	var snapshots []*meta.File
	for _, id := range order {
		f := changed[id]
		if f.Deleted {
			deleted = append(deleted, id)
			delete(pending, id)
			snapshots = append(snapshots, f)
			continue
		}
		content, etag, err := ix.fetchText(ctx, f)
		if err != nil {
			return rep, err
		}
		if content == nil && f.Derived.Text.ObjectKey != "" {
			pending[id] = f.Derived.Text.ObjectKey
		} else {
			delete(pending, id)
			if content != nil {
				f.Derived.Text.IndexedETag = etag
				f.Derived.Text.Status = "ready"
				applyExtractedMetadata(f, content)
			}
		}
		docs = append(docs, sidecar.BuildDocs(f, content, ix.cfg.Limits)...)
		snapshots = append(snapshots, f)
	}
	if err := ix.search.Delete(ctx, deleted); err != nil {
		return rep, err
	}
	if err := ix.search.Upsert(ctx, docs); err != nil {
		return rep, err
	}
	committed, err := ix.search.Commit(ctx)
	if err != nil {
		return rep, err
	}
	ix.markUnpublished(order, false)
	for _, f := range snapshots {
		ix.metas.Put(f)
		if err := meta.WriteSnapshot(ctx, ix.store, ix.cfg.Bucket, ix.cfg.Prefix, f); err != nil {
			return rep, fmt.Errorf("write snapshot %s: %w", f.FileID, err)
		}
		if f.Deleted {
			rep.FilesDeleted++
		} else {
			rep.FilesIndexed++
		}
	}
	ix.setLast(lastKey, pending)
	if err := ix.saveState(); err != nil {
		return rep, err
	}
	rep.PendingText = len(pending)
	if err := ix.publishPendingLocked(ctx, &rep, &committed); err != nil {
		return rep, err
	}
	rep.Duration = time.Since(started)
	return rep, nil
}

func (ix *Indexer) setLast(lastKey string, pending map[string]string) {
	ix.stateMu.Lock()
	ix.state.LastJournalKey = lastKey
	ix.state.PendingText = pending
	ix.stateMu.Unlock()
}

// markUnpublished records that the local index is ahead of the bucket. ids
// are the files whose documents changed; full means every file (a rebuild,
// or an unknown set after a crash).
func (ix *Indexer) markUnpublished(ids []string, full bool) {
	if ix.snap == nil {
		return
	}
	ix.stateMu.Lock()
	ix.unpublished = true
	if full {
		ix.unpubFull = true
		ix.unpubIDs = nil
	} else if !ix.unpubFull {
		ix.unpubIDs = append(ix.unpubIDs, ids...)
	}
	ix.stateMu.Unlock()
}

// publishPendingLocked publishes when the local index is ahead of the
// bucket. files is the result of the commit that just happened, or nil.
func (ix *Indexer) publishPendingLocked(ctx context.Context, rep *Report, files *sidecar.IndexFiles) error {
	if ix.snap == nil {
		return nil
	}
	ix.stateMu.RLock()
	pending := ix.unpublished
	ix.stateMu.RUnlock()
	if !pending {
		rep.Generation = ix.Generation()
		return nil
	}
	p, err := ix.publishLocked(ctx, files)
	if err != nil {
		ix.stateMu.Lock()
		ix.publishErr = err.Error()
		ix.stateMu.Unlock()
		return fmt.Errorf("publish index snapshot: %w", err)
	}
	rep.Published = true
	rep.Generation = p.Generation
	return nil
}

func (ix *Indexer) publishLocked(ctx context.Context, files *sidecar.IndexFiles) (*snapshot.Pointer, error) {
	if files == nil {
		f, err := ix.search.Files(ctx)
		if err != nil {
			return nil, err
		}
		files = &f
	}
	// The bucket's pointer is the base of the next generation. Normally it
	// is the one this process wrote; another writer is a deployment error,
	// but its pointer is still continued from rather than overwritten
	// blindly, so file bookkeeping stays consistent.
	prev, prevETag := ix.pointer, ix.pointerETag
	etag, found, err := ix.snap.HeadPointer(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case !found && prev != nil:
		ix.logger.Warn("index_snapshot_pointer_missing_republishing", "local_generation", prev.Generation)
		prev = nil
	case found && (prev == nil || etag != prevETag):
		p, _, found2, err := ix.snap.ReadPointer(ctx)
		if err != nil {
			return nil, err
		}
		if found2 {
			if prev != nil {
				ix.logger.Warn("index_snapshot_pointer_changed_by_another_writer", "local_generation", prev.Generation, "published_generation", p.Generation)
			}
			prev = p
		}
	}
	ix.stateMu.RLock()
	pub := snapshot.Publication{
		MetaJSON:       files.MetaJSON,
		Files:          files.Files,
		Opstamp:        files.Opstamp,
		NumDocs:        files.NumDocs,
		SchemaVersion:  ix.search.SchemaVersion(),
		Tokenizer:      ix.search.Tokenizer,
		LastJournalKey: ix.state.LastJournalKey,
		PendingText:    ix.state.PendingText,
		Changed:        ix.unpubIDs,
		Full:           ix.unpubFull,
	}
	ix.stateMu.RUnlock()
	p, newETag, err := ix.snap.Publish(ctx, pub, prev, ix.cfg.IndexDir, ix.cfg.GCGrace)
	if errors.Is(err, snapshot.ErrLocalFileMissing) {
		// The directory changed since the file list was taken; ask again.
		ix.logger.Warn("index_snapshot_file_list_stale_retrying", "error", err)
		f, ferr := ix.search.Files(ctx)
		if ferr != nil {
			return nil, ferr
		}
		pub.MetaJSON, pub.Files, pub.Opstamp, pub.NumDocs = f.MetaJSON, f.Files, f.Opstamp, f.NumDocs
		p, newETag, err = ix.snap.Publish(ctx, pub, prev, ix.cfg.IndexDir, ix.cfg.GCGrace)
	}
	if err != nil {
		return nil, err
	}
	ix.pointer, ix.pointerETag = p, newETag
	ix.stateMu.Lock()
	ix.state.Generation = p.Generation
	ix.unpublished, ix.unpubIDs, ix.unpubFull, ix.publishErr = false, nil, false, ""
	ix.stateMu.Unlock()
	if err := ix.saveState(); err != nil {
		return nil, err
	}
	if n, err := ix.snap.SweepOrphans(ctx, p, ix.cfg.GCGrace); err != nil {
		ix.logger.Warn("index_snapshot_sweep_failed", "error", err)
	} else if n > 0 {
		ix.logger.Info("index_snapshot_orphans_deleted", "count", n)
	}
	if _, err := snapshot.Prune(ix.cfg.IndexDir, p.Files); err != nil {
		ix.logger.Warn("index_dir_prune_failed", "error", err)
	}
	return p, nil
}

// fetchText reads the file's .text.json. A missing object is not an error:
// extraction may still be running.
func (ix *Indexer) fetchText(ctx context.Context, f *meta.File) (*extraction.Content, string, error) {
	key := f.Derived.Text.ObjectKey
	if key == "" {
		return nil, "", nil
	}
	out, err := ix.store.GetObject(ctx, storage.GetInput{Bucket: ix.cfg.Bucket, Key: key})
	if err != nil {
		if objerr.IsNotFound(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("get text %s: %w", key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, ix.cfg.MaxTextBytes))
	if err != nil {
		return nil, "", err
	}
	var content extraction.Content
	if err := json.Unmarshal(body, &content); err != nil {
		ix.logger.Warn("text_json_invalid", "key", key, "error", err)
		return &extraction.Content{}, out.ETag, nil
	}
	return &content, out.ETag, nil
}

func (ix *Indexer) rebuildLocked(ctx context.Context) (Report, error) {
	started := time.Now()
	rep := Report{Rebuilt: true}
	ix.logger.Info("index_rebuild_start", "tenant", ix.cfg.Tenant)
	if err := ix.search.Reset(ctx); err != nil {
		return rep, fmt.Errorf("reset index: %w", err)
	}
	fresh := meta.NewCache()
	if _, err := fresh.LoadSnapshots(ctx, ix.store, ix.cfg.Bucket, ix.cfg.Prefix, ix.cfg.Tenant); err != nil {
		return rep, fmt.Errorf("load snapshots: %w", err)
	}
	pending := map[string]string{}
	lastKey := ""
	var batch []sidecar.Doc
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := ix.search.Upsert(ctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for _, f := range fresh.All() {
		if f.LastEvent > lastKey {
			lastKey = f.LastEvent
		}
		ix.metas.Put(f)
		if f.Deleted {
			continue
		}
		content, etag, err := ix.fetchText(ctx, f)
		if err != nil {
			return rep, err
		}
		if content == nil && f.Derived.Text.ObjectKey != "" {
			pending[f.FileID] = f.Derived.Text.ObjectKey
		} else if content != nil {
			f.Derived.Text.IndexedETag = etag
			f.Derived.Text.Status = "ready"
			applyExtractedMetadata(f, content)
		}
		batch = append(batch, sidecar.BuildDocs(f, content, ix.cfg.Limits)...)
		rep.FilesIndexed++
		if len(batch) >= 500 {
			if err := flush(); err != nil {
				return rep, err
			}
		}
	}
	if err := flush(); err != nil {
		return rep, err
	}
	if _, err := ix.search.Commit(ctx); err != nil {
		return rep, err
	}
	// Everything changed from a follower's point of view.
	ix.markUnpublished(nil, true)
	// Snapshots that are missing from the cache were never written; the
	// journal after the newest snapshot event fills the gap.
	ix.setLast(lastKey, pending)
	if err := ix.saveState(); err != nil {
		return rep, err
	}
	rep.PendingText = len(pending)
	tail, err := ix.runOnceLocked(ctx)
	rep.Events = tail.Events
	rep.FilesIndexed += tail.FilesIndexed
	rep.FilesDeleted += tail.FilesDeleted
	rep.PendingText = tail.PendingText
	rep.Published = tail.Published
	rep.Generation = tail.Generation
	rep.Duration = time.Since(started)
	return rep, err
}
