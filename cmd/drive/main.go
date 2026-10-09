// Command drive runs the Drive: streamuploader's upload and file routes, the
// Drive API and web UI, the indexer and the search sidecar, in one process
// or split into servers and one indexer that share the index through a
// snapshot in the bucket. See .knowledge/concepts/system/drive-server.yaml.
//
//	drive              serve: all-in-one (server plus indexer loop)
//	drive server       server only: serves the index snapshot an indexer published (read-only index)
//	drive indexer      indexer only: folds the journal and publishes the snapshot in a loop
//	drive reindex      rebuild the search index from the bucket, publish it and exit
//	drive index-once   fold pending journal events, publish and exit
//	drive verify       walk the audit checkpoint chain and report tampering (WORM mode)
//	drive init         write the deployment files for a target (see drive/deploy)
//
// The process is a Popcorn Web application: the listener, the request chain
// and the operational endpoints are configured through config.{APP_ENV}.toml,
// environment variables and command-line options (run with --help). The port
// is server.port, PORT or --port. Streamuploader's SU_* environment variables
// configure the object store and the upload policy; object locks for
// originals, journal and checkpoints come from SU_OBJECT_LOCK_MODE
// (governance, compliance, legal_hold) and SU_OBJECT_LOCK_RETENTION. The
// Drive's own settings are the [drive] table, each also an environment
// variable and a --drive-<key> option:
//
//	drive.tenant              DRIVE_TENANT              tenant id (default "default")
//	drive.prefix              DRIVE_PREFIX              key prefix of Drive objects in the bucket (default "drive/")
//	drive.index_dir           DRIVE_INDEX_DIR           local directory of the tantivy index (default .cache/drive/index)
//	drive.search_bin          DRIVE_SEARCH_BIN          path of the drivesearch sidecar (default: next to this binary, then PATH)
//	drive.search_tokenizer    DRIVE_SEARCH_TOKENIZER    lindera (default) or ngram
//	drive.index_interval      DRIVE_INDEX_INTERVAL      indexer period (default 30s)
//	drive.index_snapshot      DRIVE_INDEX_SNAPSHOT      true (default) keeps the index published under search/{tenant}/ in the
//	                                                    bucket; false keeps it local to the index directory (all-in-one only)
//	drive.snapshot_refresh    DRIVE_SNAPSHOT_REFRESH    server mode: how often the published pointer is checked (default 10s)
//	drive.snapshot_gc_grace   DRIVE_SNAPSHOT_GC_GRACE   how long unreferenced segment files stay in the bucket (default 15m)
//	drive.delivery            DRIVE_DELIVERY            proxy (default) or presigned
//	drive.storage             DRIVE_STORAGE             s3 (default) or memory for a throwaway local run (all-in-one only)
//	drive.worm_mode           DRIVE_WORM_MODE           off (default), append_only or strict; see README "WORM audit mode"
//	drive.worm_access_window  DRIVE_WORM_ACCESS_WINDOW  window in which repeated reads of one object by one client
//	                                                    produce a single file.accessed event (default 1m; 0 logs every request)
//
// `drive init` renders deployment files from its own flags and needs neither
// an environment nor an object store, so it is answered before the framework
// reads any configuration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/drive/audit"
	"streamuploader/drive/indexer"
	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	driveserver "streamuploader/drive/server"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/snapshot"
	"streamuploader/drive/worm"
	"streamuploader/internal/config"
	"streamuploader/internal/framework"
	suserver "streamuploader/internal/server"
	"streamuploader/internal/storage"
)

// Config is the Drive's own configuration, the [drive] table.
type Config struct {
	Tenant           string        `default:"default" help:"tenant id"`
	Prefix           string        `default:"drive/" help:"key prefix of Drive objects in the bucket"`
	IndexDir         string        `default:".cache/drive/index" help:"local directory of the tantivy index"`
	SearchBin        string        `help:"path of the drivesearch sidecar; empty looks next to this binary, then on PATH"`
	SearchTokenizer  string        `default:"lindera" enum:"lindera,ngram" help:"tokenizer of the search index"`
	IndexInterval    time.Duration `default:"30s" help:"indexer period"`
	IndexSnapshot    bool          `default:"true" help:"publish the index to the bucket after every commit and adopt it at start; false keeps it local (all-in-one only)"`
	SnapshotRefresh  time.Duration `default:"10s" help:"server mode: how often the published pointer is checked"`
	SnapshotGCGrace  time.Duration `default:"15m" help:"how long unreferenced segment files stay in the bucket"`
	Delivery         string        `default:"proxy" enum:"proxy,presigned" help:"how file bytes reach the browser"`
	Storage          string        `default:"s3" enum:"s3,memory" help:"object store; memory is a throwaway local run (all-in-one only)"`
	WormMode         string        `default:"off" enum:"off,append_only,strict" help:"WORM audit mode"`
	WormAccessWindow time.Duration `default:"1m" help:"window in which repeated reads of one object by one client produce a single file.accessed event; 0 logs every request"`
}

// serverCommand is `drive server`: serve the published index snapshot with
// a read-only index and no indexer.
type serverCommand struct{}

// indexerCommand is `drive indexer`: fold the journal and publish the
// snapshot in a loop, without an HTTP listener.
type indexerCommand struct{}

// reindexCommand is `drive reindex`: rebuild the search index from the
// bucket, publish it and exit.
type reindexCommand struct{}

// indexOnceCommand is `drive index-once`: fold pending journal events,
// publish and exit.
type indexOnceCommand struct{}

// verifyCommand is `drive verify`: walk the audit checkpoint chain and
// report tampering (WORM mode). Reads only; no sidecar needed.
type verifyCommand struct {
	Hash bool `help:"re-read every registered original and compare its SHA-256 with the checksum recorded at upload"`
	JSON bool `help:"print the report as JSON"`
}

// command names the mode the process runs in.
type command string

const (
	commandServe     command = "serve"
	commandServer    command = "server"
	commandIndexer   command = "indexer"
	commandReindex   command = "reindex"
	commandIndexOnce command = "index-once"
	commandVerify    command = "verify"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		// init renders files from flags and must not require an environment
		// or an object store.
		if err := runInit(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			var exit exitError
			if errors.As(err, &exit) {
				fmt.Fprintln(os.Stderr, "drive:", err)
				os.Exit(exit.code)
			}
			fmt.Fprintln(os.Stderr, "drive:", err)
			os.Exit(1)
		}
		return
	}
	if err := pw.SetOpenAPIInfo(pw.OpenAPIInfo{Title: "streamuploader Drive API", Version: "0.1.0"}); err != nil {
		slog.Error("drive_openapi_info", "error", err)
		os.Exit(1)
	}
	pw.RegisterConfig[Config]("drive")
	pw.RegisterSubCommand[serverCommand]("server", "server only: serve the index snapshot an indexer published")
	pw.RegisterSubCommand[indexerCommand]("indexer", "indexer only: fold the journal and publish the snapshot in a loop")
	pw.RegisterSubCommand[reindexCommand]("reindex", "rebuild the search index from the bucket, publish it and exit")
	pw.RegisterSubCommand[indexOnceCommand]("index-once", "fold pending journal events, publish and exit")
	pw.RegisterSubCommand[verifyCommand]("verify", "walk the audit checkpoint chain and report tampering (WORM mode)")
	if err := run(); err != nil {
		framework.Exit(err)
	}
}

// selectedCommand reports which subcommand the command line named; none is
// the all-in-one serve.
func selectedCommand() (command, verifyCommand) {
	if _, ok := pw.Command[serverCommand](); ok {
		return commandServer, verifyCommand{}
	}
	if _, ok := pw.Command[indexerCommand](); ok {
		return commandIndexer, verifyCommand{}
	}
	if _, ok := pw.Command[reindexCommand](); ok {
		return commandReindex, verifyCommand{}
	}
	if _, ok := pw.Command[indexOnceCommand](); ok {
		return commandIndexOnce, verifyCommand{}
	}
	if verify, ok := pw.Command[verifyCommand](); ok {
		return commandVerify, verify
	}
	return commandServe, verifyCommand{}
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
	mode, verify := selectedCommand()
	logger := slog.Default()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	memory := drive.Storage == "memory"
	if memory && (mode == commandServer || mode == commandIndexer) {
		return errors.New("drive.storage=memory keeps objects inside one process; `server` and `indexer` need a shared object store")
	}
	var store storage.Store
	if memory {
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
	wormMode, err := worm.ParseMode(drive.WormMode)
	if err != nil {
		return fmt.Errorf("drive.worm_mode: %w", err)
	}
	accessWindow := drive.WormAccessWindow
	if wormMode.Enabled() {
		// Nothing stored may be removed through streamuploader's backend
		// control API either.
		cfg.WORMMode = true
		enforcement := "application and checkpoint only (no storage lock configured)"
		if cfg.ObjectLock.Enabled() {
			enforcement = "application, checkpoint and storage lock (" + cfg.ObjectLock.String() + ")"
		}
		logger.Warn("drive_worm_mode", "mode", wormMode.String(), "enforcement", enforcement, "read_logging", readLogging(drive.Delivery), "access_window", accessWindow)
	} else if cfg.ObjectLock.Enabled() {
		logger.Info("drive_object_lock", "lock", cfg.ObjectLock.String(), "note", "originals are locked; set drive.worm_mode for journal locks, checkpoints and access logging")
	}
	indexDir := drive.IndexDir
	if memory {
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
	if mode == commandServer && !drive.IndexSnapshot {
		return errors.New("`server` follows the snapshot an indexer publishes; drive.index_snapshot must not be false")
	}

	search := sidecar.New(searchBin, indexDir)
	search.Tokenizer = drive.SearchTokenizer
	search.Logger = logger
	defer search.Close()

	jstore := &journal.Store{Objects: store, Bucket: cfg.Bucket, Prefix: prefix}
	if wormMode.Enabled() {
		jstore.Lock = cfg.ObjectLock
	}
	if mode == commandVerify {
		return runVerify(ctx, verify, store, jstore, cfg.Bucket, prefix, drive.Tenant, cfg.ObjectLock)
	}
	metas := meta.NewCache()
	serverCfg := driveserver.Config{
		Tenant:       drive.Tenant,
		Bucket:       cfg.Bucket,
		Prefix:       prefix,
		Delivery:     drive.Delivery,
		PresignTTL:   cfg.PresignTTL,
		WORM:         wormMode,
		AccessWindow: accessWindow,
		ObjectLock:   cfg.ObjectLock,
	}
	serve := func(control driveserver.IndexerControl, chain *audit.Chain) error {
		return serveHTTP(ctx, cfg, serverCfg, store, jstore, metas, search, control, chain, logger)
	}

	if mode == commandServer {
		follower := snapshot.NewFollower(&snapshot.Store{Objects: store, Bucket: cfg.Bucket, Prefix: prefix, Tenant: drive.Tenant, Logger: logger}, search, metas, indexDir, logger)
		if err := follower.Start(ctx); err != nil {
			return fmt.Errorf("start snapshot follower: %w", err)
		}
		logger.Info("drive_snapshot_ready", "generation", follower.Generation(), "files", metas.Len(), "refresh", drive.SnapshotRefresh)
		go follower.RunLoop(ctx, drive.SnapshotRefresh)
		// The checkpoint endpoints read the chain the indexer writes.
		var chain *audit.Chain
		if wormMode.Enabled() {
			chain = &audit.Chain{Objects: store, Bucket: cfg.Bucket, Prefix: prefix, Tenant: drive.Tenant, Lock: jstore.Lock}
		}
		return serve(follower, chain)
	}

	ix := indexer.New(indexer.Config{Tenant: drive.Tenant, Bucket: cfg.Bucket, Prefix: prefix, IndexDir: indexDir, Snapshot: drive.IndexSnapshot, GCGrace: drive.SnapshotGCGrace, Checkpoints: wormMode.Enabled(), Lock: jstore.Lock}, store, jstore, search, metas, logger)
	switch mode {
	case commandReindex:
		if _, err := ix.Start(ctx); err != nil {
			return err
		}
		rep, err := ix.Rebuild(ctx)
		if err != nil {
			return err
		}
		logger.Info("reindex_done", "files", rep.FilesIndexed, "pending_text", rep.PendingText, "published", rep.Published, "generation", rep.Generation, "checkpoint", rep.Checkpoint, "duration", rep.Duration)
		return nil
	case commandIndexOnce:
		rep, err := ix.Start(ctx)
		if err != nil {
			return err
		}
		logger.Info("index_once_done", "events", rep.Events, "indexed", rep.FilesIndexed, "deleted", rep.FilesDeleted, "pending_text", rep.PendingText, "rebuilt", rep.Rebuilt, "adopted", rep.Adopted, "published", rep.Published, "generation", rep.Generation, "checkpoint", rep.Checkpoint)
		return nil
	}

	rep, err := ix.Start(ctx)
	if err != nil {
		return fmt.Errorf("start indexer: %w", err)
	}
	logger.Info("drive_index_ready", "files", metas.Len(), "rebuilt", rep.Rebuilt, "adopted", rep.Adopted, "pending_text", rep.PendingText, "generation", rep.Generation, "snapshot", drive.IndexSnapshot)
	if mode == commandIndexer {
		logger.Info("drive_indexer_loop", "tenant", drive.Tenant, "prefix", prefix, "index_dir", indexDir, "interval", drive.IndexInterval)
		ix.RunLoop(ctx, drive.IndexInterval)
		return nil
	}
	go ix.RunLoop(ctx, drive.IndexInterval)
	return serve(ix, ix.Chain())
}

// serveHTTP mounts streamuploader and the Drive API on one mux and serves it
// under the framework chain: pw.Run binds the configured port and shuts down
// gracefully on SIGINT/SIGTERM; the indexer or follower loop follows ctx.
func serveHTTP(ctx context.Context, cfg config.Config, serverCfg driveserver.Config, store storage.Store, jstore *journal.Store, metas *meta.Cache, search *sidecar.Client, control driveserver.IndexerControl, chain *audit.Chain, logger *slog.Logger) error {
	uploader := suserver.New(cfg, store)
	drive := driveserver.New(serverCfg, driveserver.Deps{
		Store:    store,
		Journal:  jstore,
		Metas:    metas,
		Search:   search,
		Indexer:  control,
		Uploader: uploader.Handler(),
		Logger:   logger,
		Chain:    chain,
	})
	logger.Info("drive_starting", "tenant", serverCfg.Tenant, "prefix", serverCfg.Prefix, "index_dir", search.IndexDir, "search_bin", search.Binary, "delivery", serverCfg.Delivery, "worm", serverCfg.WORM.String())
	return pw.Run(ctx, drive.Handler())
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
func runVerify(ctx context.Context, options verifyCommand, store storage.Store, jstore *journal.Store, bucket, prefix, tenant string, lock storage.LockPolicy) error {
	chain := &audit.Chain{Objects: store, Bucket: bucket, Prefix: prefix, Tenant: tenant, Lock: lock}
	rep, err := chain.Verify(ctx, jstore, audit.VerifyOptions{HashOriginals: options.Hash})
	if err != nil {
		return err
	}
	if options.JSON {
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
