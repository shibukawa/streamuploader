// Package server is the Drive's HTTP surface: the /api/drive API, the web UI
// and the pass-through of streamuploader's upload and file routes, all on
// one origin. See .knowledge/concepts/api/drive-api.yaml.
package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"streamuploader/drive/audit"
	"streamuploader/drive/journal"
	"streamuploader/drive/meta"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/ui"
	"streamuploader/drive/worm"
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
	// OverlayTTL caches the recent-journal listing between requests.
	OverlayTTL time.Duration
	MaxOverlay int
	// Actor is recorded on events until there are users.
	Actor string
	// WORM is the write-once mode; Off keeps the normal editable Drive.
	WORM worm.Mode
	// AccessWindow collapses repeated reads of one object by one client
	// (range requests of a viewer) into a single file.accessed event; zero
	// records every request.
	AccessWindow time.Duration
	// ObjectLock is reported by the info endpoint so a WORM deployment
	// states which storage lock it relies on.
	ObjectLock storage.LockPolicy
}

// IndexerControl is what the server needs from the indexer.
type IndexerControl interface {
	LastJournalKey() string
	Poke()
	RebuildAsync()
}

type Deps struct {
	Store    storage.Store
	Journal  *journal.Store
	Metas    *meta.Cache
	Search   *sidecar.Client
	Indexer  IndexerControl
	Uploader http.Handler
	Logger   *slog.Logger
	// Chain serves the checkpoint endpoints; nil without WORM mode.
	Chain *audit.Chain
}

type Server struct {
	cfg     Config
	deps    Deps
	logger  *slog.Logger
	overlay overlayCache
	access  accessLog
}

func New(cfg Config, deps Deps) *Server {
	if cfg.Delivery == "" {
		cfg.Delivery = "proxy"
	}
	if cfg.PresignTTL <= 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.OverlayTTL <= 0 {
		cfg.OverlayTTL = 2 * time.Second
	}
	if cfg.MaxOverlay <= 0 {
		cfg.MaxOverlay = 500
	}
	if cfg.Actor == "" {
		cfg.Actor = "local"
	}
	if cfg.WORM == "" {
		cfg.WORM = worm.Off
	}
	if cfg.WORM.Enabled() && cfg.Delivery == "presigned" && cfg.PresignTTL > maxWORMPresignTTL {
		// A presigned URL is an unlogged read for as long as it lives.
		cfg.PresignTTL = maxWORMPresignTTL
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Server{cfg: cfg, deps: deps, logger: deps.Logger, access: accessLog{seen: map[string]time.Time{}}}
}

// maxWORMPresignTTL bounds presigned URLs in WORM mode.
const maxWORMPresignTTL = 5 * time.Minute

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
	mux.HandleFunc("GET /api/drive/info", s.info)
	mux.HandleFunc("POST /api/drive/files", s.registerFile)
	mux.HandleFunc("GET /api/drive/files/{id}", s.getFile)
	mux.HandleFunc("PATCH /api/drive/files/{id}", s.patchFile)
	mux.HandleFunc("DELETE /api/drive/files/{id}", s.deleteFile)
	mux.HandleFunc("POST /api/drive/files/{id}/restore", s.restoreFile)
	mux.HandleFunc("GET /api/drive/files/{id}/versions", s.listVersions)
	mux.HandleFunc("POST /api/drive/files/{id}/versions", s.newVersion)
	mux.HandleFunc("GET /api/drive/files/{id}/versions/{n}/content", func(w http.ResponseWriter, r *http.Request) { s.deliverVersion(w, r, false) })
	mux.HandleFunc("GET /api/drive/files/{id}/versions/{n}/download", func(w http.ResponseWriter, r *http.Request) { s.deliverVersion(w, r, true) })
	mux.HandleFunc("GET /api/drive/files/{id}/content", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "content") })
	mux.HandleFunc("GET /api/drive/files/{id}/download", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "download") })
	mux.HandleFunc("GET /api/drive/files/{id}/preview", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "preview") })
	mux.HandleFunc("GET /api/drive/files/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) { s.deliver(w, r, "thumbnail") })
	mux.HandleFunc("GET /api/drive/search", s.search)
	mux.HandleFunc("GET /api/drive/facets", s.facets)
	mux.HandleFunc("GET /api/drive/stats", s.stats)
	mux.HandleFunc("GET /api/drive/journal", s.exportJournal)
	mux.HandleFunc("GET /api/drive/checkpoints", s.listCheckpoints)
	mux.HandleFunc("GET /api/drive/checkpoints/{n}", s.getCheckpoint)
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

// writeReadonly answers a change that WORM mode refuses.
func writeReadonly(w http.ResponseWriter, err error) {
	msg := "this deployment runs in WORM mode"
	var ro *worm.ErrReadonly
	if errors.As(err, &ro) {
		msg = ro.Reason
	}
	writeError(w, http.StatusMethodNotAllowed, "worm_readonly", msg)
}

// info describes the deployment to clients: the UI hides what WORM mode
// refuses, and an auditor sees which storage lock the deployment relies on.
func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	lock := map[string]any{"mode": s.cfg.ObjectLock.Mode, "description": s.cfg.ObjectLock.String()}
	if s.cfg.ObjectLock.Period > 0 {
		lock["retention"] = s.cfg.ObjectLock.Period.String()
	}
	enforcement := "none"
	if s.cfg.WORM.Enabled() {
		enforcement = "application and checkpoint only"
		if s.cfg.ObjectLock.Enabled() {
			enforcement = "application, checkpoint and storage lock"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":   s.cfg.Tenant,
		"delivery": s.cfg.Delivery,
		"actor":    s.cfg.Actor,
		"worm": map[string]any{
			"mode":          s.cfg.WORM.String(),
			"enabled":       s.cfg.WORM.Enabled(),
			"enforcement":   enforcement,
			"access_window": s.cfg.AccessWindow.String(),
			"read_logging":  s.readLogging(),
			"presign_ttl":   s.cfg.PresignTTL.String(),
			"object_lock":   lock,
		},
	})
}

func (s *Server) readLogging() string {
	if !s.cfg.WORM.Enabled() {
		return "off"
	}
	if s.cfg.Delivery == "presigned" {
		return "presigned_with_event"
	}
	return "proxy"
}
