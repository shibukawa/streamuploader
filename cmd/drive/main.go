// Command drive runs the Drive in all-in-one mode: streamuploader's upload and
// file routes, the Drive API and web UI, the indexer loop and the search
// sidecar, all in one process. See .knowledge/concepts/system/drive-server.yaml.
//
//	drive              serve (all-in-one)
//	drive reindex      rebuild the search index from the bucket and exit
//	drive index-once   fold pending journal events and exit
//
// The process is a Popcorn Web application: the listener, the request chain
// and the operational endpoints are configured through config.{APP_ENV}.toml,
// environment variables and command-line options (run with --help). The port
// is server.port, PORT or --port. Streamuploader's SU_* environment variables
// configure the object store and the upload policy. The Drive's own settings
// are the [drive] table:
//
//	drive.tenant            DRIVE_TENANT            tenant id (default "default")
//	drive.prefix            DRIVE_PREFIX            key prefix of Drive objects in the bucket (default "drive/")
//	drive.index_dir         DRIVE_INDEX_DIR         local directory of the tantivy index (default .cache/drive/index)
//	drive.search_bin        DRIVE_SEARCH_BIN        path of the drivesearch sidecar (default: next to this binary, then PATH)
//	drive.search_tokenizer  DRIVE_SEARCH_TOKENIZER  lindera (default) or ngram
//	drive.index_interval    DRIVE_INDEX_INTERVAL    indexer period (default 30s)
//	drive.delivery          DRIVE_DELIVERY          proxy (default) or presigned
//	drive.storage           DRIVE_STORAGE           s3 (default) or memory for a throwaway local run
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/drive/indexer"
	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	driveserver "streamuploader/drive/server"
	"streamuploader/drive/sidecar"
	"streamuploader/internal/config"
	"streamuploader/internal/framework"
	suserver "streamuploader/internal/server"
	"streamuploader/internal/storage"
)

// Config is the Drive's own configuration, the [drive] table.
type Config struct {
	Tenant          string        `default:"default" help:"tenant id"`
	Prefix          string        `default:"drive/" help:"key prefix of Drive objects in the bucket"`
	IndexDir        string        `default:".cache/drive/index" help:"local directory of the tantivy index"`
	SearchBin       string        `help:"path of the drivesearch sidecar; empty looks next to this binary, then on PATH"`
	SearchTokenizer string        `default:"lindera" enum:"lindera,ngram" help:"tokenizer of the search index"`
	IndexInterval   time.Duration `default:"30s" help:"indexer period"`
	Delivery        string        `default:"proxy" enum:"proxy,presigned" help:"how file bytes reach the browser"`
	Storage         string        `default:"s3" enum:"s3,memory" help:"object store; memory is a throwaway local run"`
}

// reindexCommand is `drive reindex`: rebuild the search index from the bucket
// and exit.
type reindexCommand struct{}

// indexOnceCommand is `drive index-once`: fold pending journal events and exit.
type indexOnceCommand struct{}

func main() {
	if err := pw.SetOpenAPIInfo(pw.OpenAPIInfo{Title: "streamuploader Drive API", Version: "0.1.0"}); err != nil {
		slog.Error("drive_openapi_info", "error", err)
		os.Exit(1)
	}
	pw.RegisterConfig[Config]("drive")
	pw.RegisterSubCommand[reindexCommand]("reindex", "rebuild the search index from the bucket and exit")
	pw.RegisterSubCommand[indexOnceCommand]("index-once", "fold pending journal events and exit")
	if err := run(); err != nil {
		framework.Exit(err)
	}
}

func run() error {
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
	if err := framework.Prepare(framework.Options{
		MaxRequestBody:   suserver.EffectiveMaxUploadBytes(cfg),
		StreamingUploads: true,
		LegacyLogFormat:  cfg.Logging.Format,
		LegacyLogLevel:   cfg.Logging.Level,
	}); err != nil {
		return err
	}
	if handled, err := framework.RunAction(); handled {
		return err
	}
	drive := pw.ConfigContext[Config](nil)
	logger := slog.Default()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store storage.Store
	if drive.Storage == "memory" {
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

	prefix := drive.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	indexDir := drive.IndexDir
	if drive.Storage == "memory" {
		// An index left over from an earlier run would describe objects the
		// fresh in-memory bucket does not have.
		if err := os.RemoveAll(indexDir); err != nil {
			return fmt.Errorf("clear index dir: %w", err)
		}
	}
	searchBin, err := sidecar.FindBinary(drive.SearchBin)
	if err != nil {
		return err
	}

	search := sidecar.New(searchBin, indexDir)
	search.Tokenizer = drive.SearchTokenizer
	search.Logger = logger
	defer search.Close()

	jstore := &journal.Store{Objects: store, Bucket: cfg.Bucket, Prefix: prefix}
	metas := meta.NewCache()
	ix := indexer.New(indexer.Config{Tenant: drive.Tenant, Bucket: cfg.Bucket, Prefix: prefix, IndexDir: indexDir}, store, jstore, search, metas, logger)

	if _, ok := pw.Command[reindexCommand](); ok {
		if _, err := ix.Start(ctx); err != nil {
			return err
		}
		rep, err := ix.Rebuild(ctx)
		if err != nil {
			return err
		}
		logger.Info("reindex_done", "files", rep.FilesIndexed, "pending_text", rep.PendingText, "duration", rep.Duration)
		return nil
	}
	if _, ok := pw.Command[indexOnceCommand](); ok {
		rep, err := ix.Start(ctx)
		if err != nil {
			return err
		}
		logger.Info("index_once_done", "events", rep.Events, "indexed", rep.FilesIndexed, "deleted", rep.FilesDeleted, "pending_text", rep.PendingText, "rebuilt", rep.Rebuilt)
		return nil
	}

	rep, err := ix.Start(ctx)
	if err != nil {
		return fmt.Errorf("start indexer: %w", err)
	}
	logger.Info("drive_index_ready", "files", metas.Len(), "rebuilt", rep.Rebuilt, "pending_text", rep.PendingText)
	go ix.RunLoop(ctx, drive.IndexInterval)

	uploader := suserver.New(cfg, store)
	drv := driveserver.New(driveserver.Config{
		Tenant:     drive.Tenant,
		Bucket:     cfg.Bucket,
		Prefix:     prefix,
		Delivery:   drive.Delivery,
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
	logger.Info("drive_starting", "tenant", drive.Tenant, "prefix", prefix, "index_dir", indexDir, "search_bin", searchBin, "delivery", drive.Delivery)
	// pw.Run binds the configured port, serves under the framework chain and
	// shuts down gracefully on SIGINT/SIGTERM; the indexer loop follows ctx.
	return pw.Run(ctx, drv.Handler())
}
