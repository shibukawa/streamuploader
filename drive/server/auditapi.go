package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"streamuploader/drive/audit"
	"streamuploader/drive/journal"
	"streamuploader/drive/objerr"
)

// exportJournal streams events as JSON lines for audit tooling. Query:
// since and until are journal keys or event ids (exclusive lower bound,
// inclusive upper bound), type filters by event type, limit bounds the
// count (default 1000, max 10000).
func (s *Server) exportJournal(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := s.journalBound(q.Get("since"), false)
	until := s.journalBound(q.Get("until"), true)
	typ := strings.TrimSpace(q.Get("type"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	cursor := since
	written := 0
	for written < limit {
		page := 200
		if remaining := limit - written; remaining < page {
			page = remaining
		}
		keys, err := s.deps.Journal.ListKeys(r.Context(), s.cfg.Tenant, cursor, page)
		if err != nil {
			if written == 0 {
				s.fail(w, "list_journal", err)
			}
			return
		}
		for _, key := range keys {
			if until != "" && key > until {
				return
			}
			cursor = key
			e, err := s.deps.Journal.Read(r.Context(), key)
			if err != nil {
				continue
			}
			if typ != "" && string(e.Event.Type) != typ {
				continue
			}
			_ = enc.Encode(map[string]any{"key": e.Key, "sha256": e.Digest, "event": e.Event})
			written++
			if written >= limit {
				return
			}
		}
		if len(keys) < page {
			return
		}
	}
}

// journalBound turns a since/until parameter into a journal key. A full key
// is used as is; a bare event id becomes the key of that id's month.
func (s *Server) journalBound(value string, upper bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "/") {
		return value
	}
	if t, err := journal.ULIDTime(value); err == nil {
		return journal.Key(s.cfg.Prefix, s.cfg.Tenant, value, t)
	}
	// An RFC 3339 time selects the first (or last) possible key of that instant.
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		id := journal.NewULID(t)
		if !upper {
			id = id[:10] + strings.Repeat("0", 16)
		} else {
			id = id[:10] + strings.Repeat("Z", 16)
		}
		return journal.Key(s.cfg.Prefix, s.cfg.Tenant, id, t)
	}
	return value
}

type checkpointSummary struct {
	N            int64       `json:"n"`
	Key          string      `json:"key"`
	At           time.Time   `json:"at"`
	EventCount   int         `json:"event_count"`
	Originals    int         `json:"originals"`
	JournalRange audit.Range `json:"journal_range"`
	MerkleRoot   string      `json:"journal_merkle_root"`
	Prev         string      `json:"prev_checkpoint_sha256"`
}

func (s *Server) listCheckpoints(w http.ResponseWriter, r *http.Request) {
	if s.deps.Chain == nil {
		writeError(w, http.StatusNotFound, "no_checkpoints", "audit checkpoints are written in WORM mode only")
		return
	}
	keys, err := s.deps.Chain.Keys(r.Context())
	if err != nil {
		s.fail(w, "list_checkpoints", err)
		return
	}
	// Newest first, bounded; the full chain is walked by `drive verify`.
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]checkpointSummary, 0, limit)
	for i := len(keys) - 1; i >= 0 && len(out) < limit; i-- {
		cp, _, err := s.deps.Chain.ReadKey(r.Context(), keys[i])
		if err != nil {
			s.fail(w, "read_checkpoint", err)
			return
		}
		out = append(out, checkpointSummary{N: cp.N, Key: keys[i], At: cp.At, EventCount: cp.EventCount, Originals: len(cp.Originals), JournalRange: cp.JournalRange, MerkleRoot: cp.JournalMerkleRoot, Prev: cp.PrevCheckpointSHA256})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenant": s.cfg.Tenant, "total": len(keys), "genesis": audit.Genesis(s.cfg.Tenant), "checkpoints": out})
}

func (s *Server) getCheckpoint(w http.ResponseWriter, r *http.Request) {
	if s.deps.Chain == nil {
		writeError(w, http.StatusNotFound, "no_checkpoints", "audit checkpoints are written in WORM mode only")
		return
	}
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_checkpoint", "checkpoint number must be a positive integer")
		return
	}
	cp, _, err := s.deps.Chain.Read(r.Context(), n)
	if err != nil {
		if objerr.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", "checkpoint not found")
			return
		}
		s.fail(w, "read_checkpoint", err)
		return
	}
	writeJSON(w, http.StatusOK, cp)
}
