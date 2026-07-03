package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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
	if cfg.SecurityConfigPath == "" {
		slog.Info("security_config_loaded", "source", "built_in_defaults")
	} else {
		slog.Info("security_config_loaded", "source", "file", "path", cfg.SecurityConfigPath)
	}
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
	)
	logStartupFileTypePolicies(cfg.Security, cfg.Thumbnails, thumbnailPlan)
}

type fileTypePolicyLog struct {
	Type        string
	ContentType string
	Group       string
	Extension   string
	ScriptType  string
}

func logStartupFileTypePolicies(policy config.SecurityPolicy, thumbnails config.ThumbnailPolicy, plan thumbnail.Plan) {
	for _, entry := range startupFileTypes() {
		mode := resolvedSanitizationMode(entry, policy.FileSanitization)
		act := policyAction(policy, mode, entry)
		args := []any{"type", entry.Type, "act", act}
		if backend := thumbnailBackend(entry, thumbnails, plan); backend != "" {
			args = append(args, "thumbnail", backend)
		}
		slog.Info("policy", args...)
	}
}

func startupFileTypes() []fileTypePolicyLog {
	return []fileTypePolicyLog{
		{Type: "jpeg", ContentType: "image/jpeg", Group: "image"},
		{Type: "png", ContentType: "image/png", Group: "image"},
		{Type: "gif", ContentType: "image/gif", Group: "image"},
		{Type: "webp", ContentType: "image/webp", Group: "image"},
		{Type: "avif", ContentType: "image/avif", Group: "image"},
		{Type: "heif", ContentType: "image/heif", Group: "image"},
		{Type: "svg", ContentType: "image/svg+xml", Group: "svg"},
		{Type: "pdf", ContentType: "application/pdf", Group: "office_pdf"},
		{Type: "docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Group: "ooxml"},
		{Type: "xlsx", ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Group: "ooxml"},
		{Type: "pptx", ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", Group: "ooxml"},
		{Type: "doc", ContentType: "application/msword", Group: "legacy_or_complex"},
		{Type: "xls", ContentType: "application/vnd.ms-excel", Group: "legacy_or_complex"},
		{Type: "ppt", ContentType: "application/vnd.ms-powerpoint", Group: "legacy_or_complex"},
		{Type: "txt", ContentType: "text/plain", Group: "text"},
		{Type: "text", ContentType: "text/plain", Group: "text"},
		{Type: "csv", ContentType: "text/csv", Group: "text"},
		{Type: "md", ContentType: "text/markdown", Group: "markup"},
		{Type: "html", ContentType: "text/html", Group: "markup"},
		{Type: "rtf", ContentType: "application/rtf", Group: "legacy_or_complex"},
		{Type: "json", ContentType: "application/json", Group: "text"},
		{Type: "xml", ContentType: "application/xml", Group: "markup"},
		{Type: "sh", ContentType: "text/x-shellscript", Group: "script", Extension: "sh", ScriptType: "shell"},
		{Type: "py", ContentType: "text/x-python", Group: "script", Extension: "py", ScriptType: "python"},
		{Type: "js", ContentType: "text/javascript", Group: "script", Extension: "js", ScriptType: "node"},
		{Type: "mp4", ContentType: "video/mp4", Group: "video"},
		{Type: "mov", ContentType: "video/quicktime", Group: "video"},
		{Type: "elf", ContentType: "application/x-executable", Group: "executable"},
		{Type: "mach-o", ContentType: "application/x-mach-binary", Group: "executable"},
		{Type: "exe", ContentType: "application/x-dosexec", Group: "executable"},
		{Type: "octet-stream", ContentType: "application/octet-stream", Group: "generic"},
	}
}

func resolvedSanitizationMode(entry fileTypePolicyLog, policy config.FileSanitizationPolicy) string {
	if mode := perFileTypeMode(entry.ContentType, policy); mode != "" {
		return mode
	}
	if mode := perFileTypeMode(entry.Type, policy); mode != "" {
		return mode
	}
	if mode := perFileTypeMode("."+entry.Type, policy); mode != "" {
		return mode
	}
	switch entry.Group {
	case "image", "video":
		return fallbackMode(policy.ImageVideoMetadata.DefaultMode, "sanitize_metadata")
	case "svg":
		return fallbackMode(policy.SVG.DefaultMode, "reject_active_or_external_content")
	case "office_pdf", "ooxml":
		return fallbackMode(policy.OfficePDF.DefaultMode, "reject_active_content")
	case "legacy_or_complex":
		return fallbackMode(policy.LegacyOrComplexDocuments.DefaultMode, "reject")
	case "markup":
		return fallbackMode(policy.Markup.DefaultMode, "reject_active_or_external_content")
	default:
		return fallbackMode(policy.DefaultMode, "secure_default")
	}
}

func perFileTypeMode(key string, policy config.FileSanitizationPolicy) string {
	if policy.PerFileType == nil {
		return ""
	}
	if entry, ok := policy.PerFileType[strings.ToLower(strings.TrimSpace(key))]; ok {
		return strings.ToLower(strings.TrimSpace(entry.Mode))
	}
	return ""
}

func fallbackMode(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func policyAction(policy config.SecurityPolicy, mode string, entry fileTypePolicyLog) string {
	if action := mimeMagicAction(policy.MimeMagic, entry); action != "" {
		return action
	}
	return sanitizationAction(policy.FileSanitization.Enabled, mode, entry)
}

func mimeMagicAction(policy config.MimeMagicPolicy, entry fileTypePolicyLog) string {
	if entry.Group == "script" && policy.RejectScriptUploads && !scriptAllowedForStartup(policy, entry) {
		return "reject_script"
	}
	denyMIMETypes := policy.ExpandedDenyMIMETypes
	if denyMIMETypes == nil {
		denyMIMETypes = enabledMIMETypes(policy.DenyMIMETypes)
	}
	if containsString(denyMIMETypes, entry.ContentType) {
		return "reject"
	}
	allowMIMETypes := policy.ExpandedAllowMIMETypes
	if allowMIMETypes == nil {
		allowMIMETypes = enabledMIMETypes(policy.AllowMIMETypes)
	}
	if len(allowMIMETypes) > 0 && !containsString(allowMIMETypes, entry.ContentType) {
		return "reject_not_allowed"
	}
	return ""
}

func scriptAllowedForStartup(policy config.MimeMagicPolicy, entry fileTypePolicyLog) bool {
	if entry.ScriptType != "" && policy.AllowedScriptTypes[entry.ScriptType] {
		return true
	}
	if entry.Extension != "" && policy.AllowedScriptExtensions[entry.Extension] {
		return true
	}
	return false
}

func enabledMIMETypes(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value, enabled := range values {
		if enabled {
			out = append(out, strings.ToLower(strings.TrimSpace(value)))
		}
	}
	return out
}

func containsString(values []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == needle {
			return true
		}
	}
	return false
}

func sanitizationAction(enabled bool, mode string, entry fileTypePolicyLog) string {
	if entry.Group == "executable" {
		return "reject"
	}
	if !enabled {
		return "ok"
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "accept_as_is":
		return "ok"
	case "sanitize_metadata", "sanitize_when_supported":
		return "sanitize_metadata"
	case "reject":
		return "reject"
	case "reject_active_content":
		return "reject_active_content"
	case "reject_active_or_external_content":
		return "reject_active_or_external_content"
	case "reject_on_sensitive_metadata":
		return "reject_sensitive_metadata"
	case "secure_default":
		if entry.Group == "image" || entry.Group == "video" {
			return "sanitize_metadata"
		}
		return "ok"
	default:
		return "unknown"
	}
}

func thumbnailBackend(entry fileTypePolicyLog, policy config.ThumbnailPolicy, plan thumbnail.Plan) string {
	if !policy.Enabled {
		return ""
	}
	if plan.ExternalWebhook {
		return "webhook"
	}
	switch entry.Group {
	case "image":
		if internalThumbnailInput(entry.Type) {
			return "internal"
		}
		return toolThumbnailBackend(plan)
	case "svg", "office_pdf":
		return toolThumbnailBackend(plan)
	case "ooxml":
		return officeThumbnailBackend()
	case "video":
		if plan.FFmpegPath != "" {
			return "ffmpeg"
		}
	}
	return ""
}

func internalThumbnailInput(fileType string) bool {
	switch fileType {
	case "jpeg", "png", "gif", "webp":
		return true
	default:
		return false
	}
}

func toolThumbnailBackend(plan thumbnail.Plan) string {
	for _, candidate := range thumbnail.ToolCandidateSummaries(plan) {
		if strings.HasPrefix(candidate.Backend, "ffmpeg:") {
			return "ffmpeg"
		}
	}
	for _, candidate := range thumbnail.ToolCandidateSummaries(plan) {
		if strings.HasPrefix(candidate.Backend, "sips:") {
			return "sips"
		}
	}
	return "unavailable"
}

func officeThumbnailBackend() string {
	for _, name := range []string{"soffice", "libreoffice"} {
		if _, err := exec.LookPath(name); err == nil {
			return "libreoffice"
		}
	}
	return "unavailable"
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
