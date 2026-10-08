// Package indexer is the Drive's single writer: it folds journal events and
// extracted text into the tantivy index through the sidecar, keeps the meta
// cache and snapshots current, and rebuilds everything from the bucket.
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
}

// State is what the indexer has folded so far.
type State struct {
	LastJournalKey string            `json:"last_journal_key"`
	PendingText    map[string]string `json:"pending_text"`
	SchemaVersion  string            `json:"schema_version"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// Report summarizes one run.
type Report struct {
	Events       int
	FilesIndexed int
	FilesDeleted int
	PendingText  int
	Rebuilt      bool
	Duration     time.Duration
}

type Indexer struct {
	cfg     Config
	store   storage.Store
	journal *journal.Store
	search  *sidecar.Client
	metas   *meta.Cache
	logger  *slog.Logger

	runMu   sync.Mutex
	stateMu sync.RWMutex
	state   State
	poke    chan struct{}
	rebuild chan struct{}
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
	if logger == nil {
		logger = slog.Default()
	}
	return &Indexer{
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

// Start opens the sidecar and makes the index consistent with the bucket: a
// missing state file or a schema mismatch triggers a full rebuild, then the
// journal is folded up to now.
func (ix *Indexer) Start(ctx context.Context) (Report, error) {
	ix.runMu.Lock()
	defer ix.runMu.Unlock()
	needRebuild := false
	if err := ix.search.Start(ctx); err != nil {
		if errors.Is(err, sidecar.ErrSchemaMismatch) {
			ix.logger.Warn("index_schema_mismatch_rebuilding", "error", err)
			needRebuild = true
		} else {
			return Report{}, err
		}
	}
	if !needRebuild {
		found, err := ix.loadState()
		if err != nil {
			ix.logger.Warn("index_state_unreadable_rebuilding", "error", err)
			needRebuild = true
		} else if !found {
			needRebuild = true
		}
	}
	if needRebuild {
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
	return ix.runOnceLocked(ctx)
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
				ix.logger.Info("index_rebuilt", "files", rep.FilesIndexed, "pending_text", rep.PendingText, "duration", rep.Duration)
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
	if rep.Events > 0 || rep.FilesIndexed > 0 || rep.FilesDeleted > 0 {
		ix.logger.Info("index_run", "events", rep.Events, "indexed", rep.FilesIndexed, "deleted", rep.FilesDeleted, "pending_text", rep.PendingText, "duration", rep.Duration)
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
		}
		rep.PendingText = len(pending)
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
	if _, err := ix.search.Commit(ctx); err != nil {
		return rep, err
	}
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
	rep.Duration = time.Since(started)
	return rep, nil
}

func (ix *Indexer) setLast(lastKey string, pending map[string]string) {
	ix.stateMu.Lock()
	ix.state.LastJournalKey = lastKey
	ix.state.PendingText = pending
	ix.stateMu.Unlock()
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
	// Snapshots that are missing from the cache were never written; the
	// journal after the newest snapshot event fills the gap.
	ix.setLast(lastKey, pending)
	if err := ix.saveState(); err != nil {
		return rep, err
	}
	rep.PendingText = len(pending)
	tail, err := ix.runOnceLocked(ctx)
	if err != nil {
		return rep, err
	}
	rep.Events = tail.Events
	rep.FilesIndexed += tail.FilesIndexed
	rep.FilesDeleted += tail.FilesDeleted
	rep.PendingText = tail.PendingText
	rep.Duration = time.Since(started)
	return rep, nil
}
