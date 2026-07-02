package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"streamuploader/internal/config"
	"streamuploader/internal/server"
	"streamuploader/internal/storage"
	"streamuploader/internal/thumbnail"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "thumbnail-convert" {
		if err := runThumbnailConvert(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cfg := config.Load()
	configureLogging(cfg.Logging)
	logStartupConfig(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.NewS3Store(ctx, storage.S3Config{
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
		slog.Error("create_s3_store_failed", "error", err)
		os.Exit(1)
	}
	if cfg.UploadDeadlines.CleanupMode == "cleanup_once" {
		if err := server.CleanupOnce(ctx, cfg, store); err != nil {
			slog.Error("cleanup_once_failed", "error", err)
			os.Exit(1)
		}
		return
	}
	slog.Info("streamuploader_listening", "addr", cfg.Addr)
	if cfg.BackendAddr != "" {
		slog.Info("streamuploader_backend_listening", "addr", cfg.BackendAddr)
	}
	if err := server.Run(ctx, cfg, store); err != nil {
		slog.Error("server_failed", "error", err)
		os.Exit(1)
	}
}

func runThumbnailConvert(args []string) error {
	fs := flag.NewFlagSet("thumbnail-convert", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	width := fs.Int("width", 400, "thumbnail width")
	height := fs.Int("height", 400, "thumbnail height")
	fit := fs.String("fit", "contain", "contain or cover")
	format := fs.String("format", "avif", "avif, webp, or jpeg")
	losslessPolicy := fs.String("lossless-policy", "force_avif_reduction", "force_avif_reduction or webp_lossless")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.Enabled = true
	policy.Width = *width
	policy.Height = *height
	policy.Fit = *fit
	policy.PreferredFormat = *format
	policy.LosslessPolicy = *losslessPolicy
	plan := thumbnail.Configure(policy)
	body, contentType, backend, outW, outH, err := thumbnail.ConvertWithPlan(os.Stdin, policy, plan)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(body); err != nil {
		return err
	}
	_ = json.NewEncoder(os.Stderr).Encode(map[string]any{
		"content_type": contentType,
		"backend":      backend,
		"width":        outW,
		"height":       outH,
		"size_bytes":   len(body),
	})
	return nil
}

func configureLogging(policy config.LoggingPolicy) {
	var level slog.Level
	switch policy.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if policy.Format == "json" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, opts)))
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, opts)))
}

func logStartupConfig(cfg config.Config) {
	if contains(cfg.AllowedOrigins, "*") {
		slog.Warn("wildcard_allowed_origins", "message", "ALLOWED_ORIGINS contains wildcard; use explicit origins for public deployments")
	}
	if cfg.EnableSharedKey && cfg.SharedKeyTTL <= 0 {
		slog.Warn("shared_key_without_default_ttl", "message", "shared keys are bearer credentials and may not expire unless request supplies ttl_seconds or expires_at")
	}
	sMaxAge := cfg.HTTPCache.SMaxAge
	if cfg.HTTPCache.Mode == "public" && sMaxAge <= 0 {
		sMaxAge = cfg.HTTPCache.MaxAge
	}
	slog.Info("http_cache_config",
		"mode", cfg.HTTPCache.Mode,
		"max_age", cfg.HTTPCache.MaxAge.String(),
		"s_max_age", sMaxAge.String(),
		"forward_etag", cfg.HTTPCache.ForwardETag,
		"forward_last_modified", cfg.HTTPCache.ForwardLastMod,
	)
	slog.Info("logging_config", "format", cfg.Logging.Format, "level", cfg.Logging.Level)
	thumbnailPlan := thumbnail.Configure(cfg.Thumbnails)
	slog.Info("thumbnail_config",
		"enabled", cfg.Thumbnails.Enabled,
		"execution_mode", cfg.Thumbnails.ExecutionMode,
		"width", cfg.Thumbnails.Width,
		"height", cfg.Thumbnails.Height,
		"fit", cfg.Thumbnails.Fit,
		"preferred_format", cfg.Thumbnails.PreferredFormat,
		"lossless_policy", cfg.Thumbnails.LosslessPolicy,
		"object_key_suffix", cfg.Thumbnails.ObjectKeySuffix,
		"external_webhook_enabled", cfg.Thumbnails.ExternalWebhookURL != "",
		"backend", thumbnailPlan.Summary,
		"cgo_enabled", thumbnailPlan.CGOEnabled,
		"ffmpeg_path", thumbnailPlan.FFmpegPath,
		"sips_path", thumbnailPlan.SipsPath,
	)
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
