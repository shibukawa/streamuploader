package server

import (
	"context"
	"sync"
	"time"

	"streamuploader/drive/journal"
	"streamuploader/drive/meta"
)

// overlayCache holds the journal entries after the indexer's last folded key
// for a short time, so every request does not list the bucket.
type overlayCache struct {
	mu        sync.Mutex
	fetchedAt time.Time
	since     string
	states    map[string]*meta.File
	count     int
}

// overlayStates returns the newest unfolded state per file, keyed by file id.
// The map must not be modified by callers.
func (s *Server) overlayStates(ctx context.Context) (map[string]*meta.File, error) {
	since := ""
	if s.deps.Indexer != nil {
		since = s.deps.Indexer.LastJournalKey()
	}
	s.overlay.mu.Lock()
	defer s.overlay.mu.Unlock()
	if s.cfg.OverlayTTL > 0 && s.overlay.states != nil && s.overlay.since == since && time.Since(s.overlay.fetchedAt) < s.cfg.OverlayTTL {
		return s.overlay.states, nil
	}
	entries, err := s.deps.Journal.ListAfter(ctx, s.cfg.Tenant, since, s.cfg.MaxOverlay)
	if err != nil {
		return nil, err
	}
	states := make(map[string]*meta.File, len(entries))
	for _, e := range entries {
		if e.Event.IsFileState() {
			st := e.Event.State
			if st.LastEvent == "" {
				st.LastEvent = e.Key
			}
			states[st.FileID] = st
		}
	}
	if len(entries) >= s.cfg.MaxOverlay {
		s.logger.Warn("overlay_truncated", "entries", len(entries), "since", since)
	}
	s.overlay.states = states
	s.overlay.since = since
	s.overlay.fetchedAt = time.Now()
	s.overlay.count = len(entries)
	return states, nil
}

// invalidateOverlay forces the next read to list the journal again; called
// after this server wrote an event so the writer sees its own change.
func (s *Server) invalidateOverlay() {
	s.overlay.mu.Lock()
	s.overlay.fetchedAt = time.Time{}
	s.overlay.mu.Unlock()
}

// resolveFile returns the current state of a file: the overlay wins over the
// folded cache. ok is false when the file is unknown.
func (s *Server) resolveFile(ctx context.Context, fileID string) (*meta.File, bool, error) {
	states, err := s.overlayStates(ctx)
	if err != nil {
		return nil, false, err
	}
	if st, ok := states[fileID]; ok {
		return st, true, nil
	}
	if f, ok := s.deps.Metas.Get(fileID); ok {
		return f, true, nil
	}
	return nil, false, nil
}

// cachedFile is the folded state only, used to diff facet counts.
func (s *Server) cachedFile(fileID string) (*meta.File, bool) {
	return s.deps.Metas.Get(fileID)
}

func (s *Server) overlayEntryCount() int {
	s.overlay.mu.Lock()
	defer s.overlay.mu.Unlock()
	return s.overlay.count
}

// appendEvent writes an event, refreshes the overlay and pokes the indexer.
func (s *Server) appendEvent(ctx context.Context, ev *journal.Event) (string, error) {
	key, err := s.deps.Journal.Append(ctx, ev)
	if err != nil {
		return "", err
	}
	s.invalidateOverlay()
	if s.deps.Indexer != nil {
		s.deps.Indexer.Poke()
	}
	return key, nil
}
