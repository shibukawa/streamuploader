package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/tinybind-go/jsonbind"

	"streamuploader/drive/audit"
	"streamuploader/drive/journal"
	"streamuploader/drive/objerr"
)

// journalExportInput is the query of GET /api/drive/journal.
type journalExportInput struct {
	Since string `query:"since"`
	Until string `query:"until"`
	Type  string `query:"type"`
	Limit int    `query:"limit"`
}

// exportJournal streams events as JSON lines for audit tooling. Query:
// since and until are journal keys or event ids (exclusive lower bound,
// inclusive upper bound), type filters by event type, limit bounds the
// count (default 1000, max 10000). Each line carries the stored bytes of the
// event unchanged, so a consumer can verify the digest beside it.
func (s *Server) exportJournal(w http.ResponseWriter, r *http.Request) {
	in, err := pw.Parse[journalExportInput](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	since := s.journalBound(in.Since, false)
	until := s.journalBound(in.Until, true)
	typ := strings.TrimSpace(in.Type)
	limit := in.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	pw.SetRoute(w, r)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	cursor := since
	written := 0
	line := make([]byte, 0, 4096)
	for written < limit {
		page := 200
		if remaining := limit - written; remaining < page {
			page = remaining
		}
		keys, err := s.deps.Journal.ListKeys(r.Context(), s.cfg.Tenant, cursor, page)
		if err != nil {
			if written == 0 {
				s.fail(w, r, "list_journal", err)
			}
			return
		}
		for _, key := range keys {
			if until != "" && key > until {
				return
			}
			cursor = key
			e, raw, err := s.deps.Journal.ReadRaw(r.Context(), key)
			if err != nil {
				continue
			}
			if typ != "" && string(e.Event.Type) != typ {
				continue
			}
			line = append(line[:0], `{"key":`...)
			line = jsonbind.AppendString(line, e.Key)
			line = append(line, `,"sha256":`...)
			line = jsonbind.AppendString(line, e.Digest)
			line = append(line, `,"event":`...)
			line = append(line, raw...)
			line = append(line, '}', '\n')
			if _, err := w.Write(line); err != nil {
				return
			}
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

// checkpointsInput is the query of GET /api/drive/checkpoints.
type checkpointsInput struct {
	Limit int `query:"limit"`
}

func (s *Server) listCheckpoints(w http.ResponseWriter, r *http.Request) {
	if s.deps.Chain == nil {
		writeProblem(w, r, http.StatusNotFound, "no_checkpoints", "audit checkpoints are written in WORM mode only")
		return
	}
	in, err := pw.Parse[checkpointsInput](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	keys, err := s.deps.Chain.Keys(r.Context())
	if err != nil {
		s.fail(w, r, "list_checkpoints", err)
		return
	}
	// Newest first, bounded; the full chain is walked by `drive verify`.
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]checkpointSummary, 0, limit)
	for i := len(keys) - 1; i >= 0 && len(out) < limit; i-- {
		cp, _, err := s.deps.Chain.ReadKey(r.Context(), keys[i])
		if err != nil {
			s.fail(w, r, "read_checkpoint", err)
			return
		}
		out = append(out, checkpointSummary{
			N:            cp.N,
			Key:          keys[i],
			At:           cp.At.Format(time.RFC3339Nano),
			EventCount:   cp.EventCount,
			Originals:    len(cp.Originals),
			JournalRange: rangeView{First: cp.JournalRange.First, Last: cp.JournalRange.Last},
			MerkleRoot:   cp.JournalMerkleRoot,
			Prev:         cp.PrevCheckpointSHA256,
		})
	}
	pw.WriteAPI(w, r, checkpointsResponse{Tenant: s.cfg.Tenant, Total: len(keys), Genesis: audit.Genesis(s.cfg.Tenant), Checkpoints: out})
}

// getCheckpoint answers with the stored checkpoint document itself, byte for
// byte, so its SHA-256 is the one the next checkpoint links to.
func (s *Server) getCheckpoint(w http.ResponseWriter, r *http.Request) {
	if s.deps.Chain == nil {
		writeProblem(w, r, http.StatusNotFound, "no_checkpoints", "audit checkpoints are written in WORM mode only")
		return
	}
	n, err := strconv.ParseInt(pw.PathValue(r, "n"), 10, 64)
	if err != nil || n <= 0 {
		writeProblem(w, r, http.StatusBadRequest, "invalid_checkpoint", "checkpoint number must be a positive integer")
		return
	}
	_, raw, err := s.deps.Chain.Read(r.Context(), n)
	if err != nil {
		if objerr.IsNotFound(err) {
			writeProblem(w, r, http.StatusNotFound, "not_found", "checkpoint not found")
			return
		}
		s.fail(w, r, "read_checkpoint", err)
		return
	}
	pw.WriteAPI(w, r, storedDocument(raw))
}
