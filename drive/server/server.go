// Package server is the Drive's HTTP surface: the /api/drive API, the web UI
// and the pass-through of streamuploader's upload and file routes, all on
// one origin. See .knowledge/concepts/api/drive-api.yaml.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"streamuploader/drive/journal"
	"streamuploader/drive/meta"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/ui"
	"streamuploader/internal/storage"
)

type Config struct {
	Tenant string
	Bucket string
	Prefix string
	// Delivery is "proxy" (bytes flow through this server, works everywhere)
	// or "presigned" (302 to the object store; needs a browser-reachable
	// endpoint with CORS for range requests).
	Delivery   string
	PresignTTL time.Duration
	// OverlayTTL caches the recent-journal listing between requests; a
	// change written by another server becomes visible here within one
	// TTL. Zero means 2s; a negative value disables the cache (tests).
	OverlayTTL time.Duration
	MaxOverlay int
	// Actor is recorded on events until there are users.
	Actor string
}

// IndexerControl is what the server needs from whoever keeps the index: the
// in-process indexer in all-in-one mode, or the snapshot follower in server
// mode. Everything after LastJournalKey is overlaid from the journal.
type IndexerControl interface {
	LastJournalKey() string
	Poke()
}

// Rebuilder is implemented by an in-process indexer; a follower cannot
// rebuild, so the reindex endpoint answers 503 without it.
type Rebuilder interface {
	RebuildAsync()
}

// StatusReporter adds the index keeper's state to /api/drive/stats.
type StatusReporter interface {
	Status() map[string]any
}

type Deps struct {
	Store    storage.Store
	Journal  *journal.Store
	Metas    *meta.Cache
	Search   *sidecar.Client
	Indexer  IndexerControl
	Uploader http.Handler
	Logger   *slog.Logger
}

type Server struct {
	cfg     Config
	deps    Deps
	logger  *slog.Logger
	overlay overlayCache
}

func New(cfg Config, deps Deps) *Server {
	if cfg.Delivery == "" {
		cfg.Delivery = "proxy"
	}
	if cfg.PresignTTL <= 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.OverlayTTL == 0 {
		cfg.OverlayTTL = 2 * time.Second
	}
	if cfg.MaxOverlay <= 0 {
		cfg.MaxOverlay = 500
	}
	if cfg.Actor == "" {
		cfg.Actor = "local"
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Server{cfg: cfg, deps: deps, logger: deps.Logger}
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.deps.Uploader != nil {
		mux.Handle("/api/upload/", s.deps.Uploader)
		mux.Handle("/api/file/", s.deps.Uploader)
		mux.Handle("/api/files/", s.deps.Uploader)
		mux.Handle("GET /healthz", s.deps.Uploader)
	} else {
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	mux.HandleFunc("POST /api/drive/files", s.registerFile)
	mux.HandleFunc("GET /api/drive/files/{id}", s.getFile)
	mux.HandleFunc("PATCH /api/drive/files/{id}", s.patchFile)
	mux.HandleFunc("DELETE /api/drive/files/{id}", s.deleteFile)
	mux.HandleFunc("GET /api/drive/files/{id}/content", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "content") })
	mux.HandleFunc("GET /api/drive/files/{id}/download", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "download") })
	mux.HandleFunc("GET /api/drive/files/{id}/preview", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "preview") })
	mux.HandleFunc("GET /api/drive/files/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "thumbnail") })
	mux.HandleFunc("GET /api/drive/search", s.search)
	mux.HandleFunc("GET /api/drive/facets", s.facets)
	mux.HandleFunc("GET /api/drive/stats", s.stats)
	mux.HandleFunc("POST /api/drive/admin/reindex", s.reindex)
	mux.Handle("GET /ui/", http.StripPrefix("/ui/", http.FileServerFS(ui.Dist())))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(ui.IndexHTML)
	})
	return s.withAccessLog(mux)
}

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/upload/") || strings.HasPrefix(r.URL.Path, "/api/file") {
			// streamuploader logs its own routes.
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("drive_http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(started).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Code: code, Message: message})
}
