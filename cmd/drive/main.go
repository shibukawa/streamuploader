// Command drive runs the Drive in all-in-one mode: streamuploader's upload and
// file routes, the Drive API and web UI, the indexer loop and the search
// sidecar, all in one process. See .knowledge/concepts/system/drive-server.yaml.
//
//	drive              serve (all-in-one)
//	drive reindex      rebuild the search index from the bucket and exit
//	drive index-once   fold pending journal events and exit
//
// Streamuploader's SU_* environment variables configure the object store and
// upload policy. Drive-specific variables:
//
//	DRIVE_TENANT            tenant id (default "default")
//	DRIVE_PREFIX            key prefix of Drive objects in the bucket (default "drive/")
//	DRIVE_INDEX_DIR         local directory of the tantivy index (default .cache/drive/index)
//	DRIVE_SEARCH_BIN        path of the drivesearch sidecar (default: next to this binary, then PATH)
//	DRIVE_SEARCH_TOKENIZER  lindera (default) or ngram
//	DRIVE_INDEX_INTERVAL    indexer period (default 30s)
//	DRIVE_DELIVERY          proxy (default) or presigned
//	DRIVE_STORAGE           s3 (default) or memory for a throwaway local run
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"streamuploader/drive/indexer"
	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	driveserver "streamuploader/drive/server"
	"streamuploader/drive/sidecar"
	"streamuploader/internal/config"
	suserver "streamuploader/internal/server"
	"streamuploader/internal/storage"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := run(); err != nil {
		slog.Error("drive_failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	// The Drive owns the public listener; streamuploader must not proxy to
	// an application server.
	if os.Getenv("SU_APPLICATION_SERVER_URL") == "" && os.Getenv("APPLICATION_SERVER_URL") == "" {
		_ = os.Setenv("SU_APPLICATION_SERVER_URL", "none")
	}
	cfg := config.Load()
	if cfg.ApplicationServerURL == "none" {
		cfg.ApplicationServerURL = ""
	}
	cfg.Mode = "embedded_drive"
	logLevel := slog.LevelInfo
	if strings.EqualFold(cfg.Logging.Level, "debug") {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store storage.Store
	if env("DRIVE_STORAGE", "s3") == "memory" {
		logger.Warn("drive_memory_storage", "note", "objects are lost when the process exits")
		store = memstore.New()
	} else {
		s3store, err := storage.NewS3Store(ctx, storage.S3Config{
			Bucket:         cfg.Bucket,
			Endpoint:       cfg.S3Endpoint,
			Region:         cfg.S3Region,
			AccessKey:      cfg.S3AccessKey,
			SecretKey:      cfg.S3SecretKey,
			ForcePathStyle: cfg.S3ForcePathStyle,
			PublicEndpoint: cfg.S3PublicEndpoint,
			PublicRead:     cfg.S3PublicRead,
		})
		if err != nil {
			return fmt.Errorf("create s3 store: %w", err)
		}
		store = s3store
	}

	tenant := env("DRIVE_TENANT", "default")
	prefix := env("DRIVE_PREFIX", "drive/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	indexDir := env("DRIVE_INDEX_DIR", ".cache/drive/index")
	if env("DRIVE_STORAGE", "s3") == "memory" {
		// An index left over from an earlier run would describe objects the
		// fresh in-memory bucket does not have.
		if err := os.RemoveAll(indexDir); err != nil {
			return fmt.Errorf("clear index dir: %w", err)
		}
	}
	searchBin, err := sidecar.FindBinary(os.Getenv("DRIVE_SEARCH_BIN"))
	if err != nil {
		return err
	}
	interval, err := time.ParseDuration(env("DRIVE_INDEX_INTERVAL", "30s"))
	if err != nil {
		return fmt.Errorf("DRIVE_INDEX_INTERVAL: %w", err)
	}

	search := sidecar.New(searchBin, indexDir)
	search.Tokenizer = env("DRIVE_SEARCH_TOKENIZER", "lindera")
	search.Logger = logger
	defer search.Close()

	jstore := &journal.Store{Objects: store, Bucket: cfg.Bucket, Prefix: prefix}
	metas := meta.NewCache()
	ix := indexer.New(indexer.Config{Tenant: tenant, Bucket: cfg.Bucket, Prefix: prefix, IndexDir: indexDir}, store, jstore, search, metas, logger)

	switch command {
	case "reindex":
		if _, err := ix.Start(ctx); err != nil {
			return err
		}
		rep, err := ix.Rebuild(ctx)
		if err != nil {
			return err
		}
		logger.Info("reindex_done", "files", rep.FilesIndexed, "pending_text", rep.PendingText, "duration", rep.Duration)
		return nil
	case "index-once":
		rep, err := ix.Start(ctx)
		if err != nil {
			return err
		}
		logger.Info("index_once_done", "events", rep.Events, "indexed", rep.FilesIndexed, "deleted", rep.FilesDeleted, "pending_text", rep.PendingText, "rebuilt", rep.Rebuilt)
		return nil
	case "serve":
	default:
		return fmt.Errorf("unknown command %q (serve, reindex, index-once)", command)
	}

	rep, err := ix.Start(ctx)
	if err != nil {
		return fmt.Errorf("start indexer: %w", err)
	}
	logger.Info("drive_index_ready", "files", metas.Len(), "rebuilt", rep.Rebuilt, "pending_text", rep.PendingText)
	go ix.RunLoop(ctx, interval)

	uploader := suserver.New(cfg, store)
	drive := driveserver.New(driveserver.Config{
		Tenant:     tenant,
		Bucket:     cfg.Bucket,
		Prefix:     prefix,
		Delivery:   env("DRIVE_DELIVERY", "proxy"),
		PresignTTL: cfg.PresignTTL,
	}, driveserver.Deps{
		Store:    store,
		Journal:  jstore,
		Metas:    metas,
		Search:   search,
		Indexer:  ix,
		Uploader: uploader.Handler(),
		Logger:   logger,
	})
	httpServer := &http.Server{Addr: cfg.Addr, Handler: drive.Handler(), ReadHeaderTimeout: 30 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	logger.Info("drive_listening", "addr", cfg.Addr, "tenant", tenant, "prefix", prefix, "index_dir", indexDir, "search_bin", searchBin, "delivery", env("DRIVE_DELIVERY", "proxy"))
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
