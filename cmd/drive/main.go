// Command drive runs the Drive in all-in-one mode: streamuploader's upload and
// file routes, the Drive API and web UI, the indexer loop and the search
// sidecar, all in one process. See .knowledge/concepts/system/drive-server.yaml.
//
//	drive              serve (all-in-one)
//	drive reindex      rebuild the search index from the bucket and exit
//	drive index-once   fold pending journal events and exit
//	drive verify       walk the audit checkpoint chain and report tampering (WORM mode)
//	drive init         write the deployment files for a target (see drive/deploy)
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
//	DRIVE_WORM_MODE         off (default), append_only or strict; see README "WORM audit mode"
//	DRIVE_WORM_ACCESS_WINDOW window in which repeated reads of one object by one client
//	                        produce a single file.accessed event (default 1m; 0 logs every request)
//
// Object locks for originals, journal and checkpoints come from streamuploader's
// SU_OBJECT_LOCK_MODE (governance, compliance, legal_hold) and SU_OBJECT_LOCK_RETENTION.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"streamuploader/drive/audit"
	"streamuploader/drive/indexer"
	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	driveserver "streamuploader/drive/server"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/worm"
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
		var exit exitError
		if errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, "drive:", err)
			os.Exit(exit.code)
		}
		slog.Error("drive_failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "init" {
		// init renders files from flags and must not require an environment
		// or an object store.
		return runInit(os.Args[2:], os.Stdout, os.Stderr)
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
	wormMode, err := worm.ParseMode(os.Getenv("DRIVE_WORM_MODE"))
	if err != nil {
		return fmt.Errorf("DRIVE_WORM_MODE: %w", err)
	}
	accessWindow, err := time.ParseDuration(env("DRIVE_WORM_ACCESS_WINDOW", "1m"))
	if err != nil {
		return fmt.Errorf("DRIVE_WORM_ACCESS_WINDOW: %w", err)
	}
	if wormMode.Enabled() {
		// Nothing stored may be removed through streamuploader's backend
		// control API either.
		cfg.WORMMode = true
		enforcement := "application and checkpoint only (no storage lock configured)"
		if cfg.ObjectLock.Enabled() {
			enforcement = "application, checkpoint and storage lock (" + cfg.ObjectLock.String() + ")"
		}
		logger.Warn("drive_worm_mode", "mode", wormMode.String(), "enforcement", enforcement, "read_logging", readLogging(env("DRIVE_DELIVERY", "proxy")), "access_window", accessWindow)
	} else if cfg.ObjectLock.Enabled() {
		logger.Info("drive_object_lock", "lock", cfg.ObjectLock.String(), "note", "originals are locked; set DRIVE_WORM_MODE for journal locks, checkpoints and access logging")
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
	if wormMode.Enabled() {
		jstore.Lock = cfg.ObjectLock
	}
	metas := meta.NewCache()
	ix := indexer.New(indexer.Config{Tenant: tenant, Bucket: cfg.Bucket, Prefix: prefix, IndexDir: indexDir, Checkpoints: wormMode.Enabled(), Lock: jstore.Lock}, store, jstore, search, metas, logger)

	switch command {
	case "verify":
		return runVerify(ctx, os.Args[2:], store, jstore, cfg.Bucket, prefix, tenant, cfg.ObjectLock)
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
		return fmt.Errorf("unknown command %q (serve, reindex, index-once, verify, init)", command)
	}

	rep, err := ix.Start(ctx)
	if err != nil {
		return fmt.Errorf("start indexer: %w", err)
	}
	logger.Info("drive_index_ready", "files", metas.Len(), "rebuilt", rep.Rebuilt, "pending_text", rep.PendingText)
	go ix.RunLoop(ctx, interval)

	uploader := suserver.New(cfg, store)
	drive := driveserver.New(driveserver.Config{
		Tenant:       tenant,
		Bucket:       cfg.Bucket,
		Prefix:       prefix,
		Delivery:     env("DRIVE_DELIVERY", "proxy"),
		PresignTTL:   cfg.PresignTTL,
		WORM:         wormMode,
		AccessWindow: accessWindow,
		ObjectLock:   cfg.ObjectLock,
	}, driveserver.Deps{
		Store:    store,
		Journal:  jstore,
		Metas:    metas,
		Search:   search,
		Indexer:  ix,
		Uploader: uploader.Handler(),
		Logger:   logger,
		Chain:    ix.Chain(),
	})
	httpServer := &http.Server{Addr: cfg.Addr, Handler: drive.Handler(), ReadHeaderTimeout: 30 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	logger.Info("drive_listening", "addr", cfg.Addr, "tenant", tenant, "prefix", prefix, "index_dir", indexDir, "search_bin", searchBin, "delivery", env("DRIVE_DELIVERY", "proxy"), "worm", wormMode.String())
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

func readLogging(delivery string) string {
	if delivery == "presigned" {
		return "presigned_with_event"
	}
	return "proxy"
}

// runVerify walks the audit checkpoint chain of the tenant and prints what it
// finds. It exits non-zero when a checkpoint, a journal event or an original
// is missing, modified or unlinked. Reads only; no sidecar needed.
func runVerify(ctx context.Context, args []string, store storage.Store, jstore *journal.Store, bucket, prefix, tenant string, lock storage.LockPolicy) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	hash := fs.Bool("hash", false, "re-read every registered original and compare its SHA-256 with the checksum recorded at upload")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	chain := &audit.Chain{Objects: store, Bucket: bucket, Prefix: prefix, Tenant: tenant, Lock: lock}
	rep, err := chain.Verify(ctx, jstore, audit.VerifyOptions{HashOriginals: *hash})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		fmt.Printf("tenant %s: %d checkpoints (head %d), %d events verified, %d originals verified, %d events not yet checkpointed\n",
			rep.Tenant, rep.Checkpoints, rep.HeadN, rep.EventsVerified, rep.OriginalsVerified, rep.UnverifiedTail)
		for _, f := range rep.Findings {
			fmt.Println("  FAIL", f.String())
		}
		if rep.OK() {
			fmt.Println("OK")
		}
	}
	if !rep.OK() {
		return fmt.Errorf("verification found %d problem(s)", len(rep.Findings))
	}
	return nil
}
