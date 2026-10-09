// Package server is the Drive's HTTP surface: the /api/drive API, the web UI
// and the pass-through of streamuploader's upload and file routes, all on
// one origin. See .knowledge/concepts/api/drive-api.yaml.
//
// The routes are registered on a Popcorn Web mux and the handlers read and
// write through the framework's generated codecs: `go tool pw generate`
// writes the request binders and the JSON writers for the types in api.go.
// The framework request chain (request id, access log, recovery, security
// headers, body cap) is applied by the binary that serves this mux.
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/shibukawa/popcornweb/pw"

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
	// OverlayTTL caches the recent-journal listing between requests; a
	// change written by another server becomes visible here within one
	// TTL. Zero means 2s; a negative value disables the cache (tests).
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
	if cfg.OverlayTTL == 0 {
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

// healthPattern is deliberately not a literal in the registrations below: the
// upload API's generated OpenAPI fragment already declares GET /healthz, and
// the generator would merge a second literal declaration of the same route
// into a conflict that fails the whole document.
var healthPattern = "GET /healthz"

// Handler builds the routes. It is the application half of the stack: the
// serving binary wraps it in the framework chain with pw.Run.
func (s *Server) Handler() http.Handler {
	mux := pw.NewServeMux()
	if s.deps.Uploader != nil {
		mux.Handle("/api/upload/", s.deps.Uploader)
		mux.Handle("/api/file/", s.deps.Uploader)
		mux.Handle("/api/files/", s.deps.Uploader)
		mux.Handle(healthPattern, s.deps.Uploader)
	} else {
		mux.HandleFunc(healthPattern, func(w http.ResponseWriter, r *http.Request) {
			pw.WriteAPI(w, r, healthView{Status: "ok"})
		})
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
		pw.SetRoute(w, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(ui.IndexHTML)
	})
	return mux
}

// writeProblem answers with an RFC 9457 problem document carrying the Drive's
// stable error code. The framework reports a 5xx to the client as a generic
// internal problem and keeps the code and message for the log.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	pw.WriteProblem(w, r, pw.Problem{Status: status, Title: http.StatusText(status), Code: code, Message: message})
}

// writeReadonly answers a change that WORM mode refuses.
func writeReadonly(w http.ResponseWriter, r *http.Request, err error) {
	msg := "this deployment runs in WORM mode"
	var ro *worm.ErrReadonly
	if errors.As(err, &ro) {
		msg = ro.Reason
	}
	writeProblem(w, r, http.StatusMethodNotAllowed, "worm_readonly", msg)
}

// info describes the deployment to clients: the UI hides what WORM mode
// refuses, and an auditor sees which storage lock the deployment relies on.
func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	lock := objectLockView{Mode: string(s.cfg.ObjectLock.Mode), Description: s.cfg.ObjectLock.String()}
	if s.cfg.ObjectLock.Period > 0 {
		lock.Retention = s.cfg.ObjectLock.Period.String()
	}
	enforcement := "none"
	if s.cfg.WORM.Enabled() {
		enforcement = "application and checkpoint only"
		if s.cfg.ObjectLock.Enabled() {
			enforcement = "application, checkpoint and storage lock"
		}
	}
	pw.WriteAPI(w, r, infoView{
		Tenant:   s.cfg.Tenant,
		Delivery: s.cfg.Delivery,
		Actor:    s.cfg.Actor,
		WORM: wormView{
			Mode:         s.cfg.WORM.String(),
			Enabled:      s.cfg.WORM.Enabled(),
			Enforcement:  enforcement,
			AccessWindow: s.cfg.AccessWindow.String(),
			ReadLogging:  s.readLogging(),
			PresignTTL:   s.cfg.PresignTTL.String(),
			ObjectLock:   lock,
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
