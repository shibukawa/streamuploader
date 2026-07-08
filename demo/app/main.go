package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type appConfig struct {
	Addr                    string
	DataPath                string
	UploadBasePath          string
	DownloadMode            string
	S3Bucket                string
	S3DownloadEndpoint      string
	S3ForcePathStyle        bool
	PresignTTL              time.Duration
	DeleteObjectsOnDelete   bool
	StreamUploaderPublicURL string
	StreamUploaderProxyURL  string
	BackendControlURL       string
	BackendAuthToken        string
}

type fileFact struct {
	UploadKey      string `json:"upload_key"`
	OriginalName   string `json:"original_name"`
	ContentType    string `json:"content_type,omitempty"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	ObjectKey      string `json:"object_key"`
	DisplayKey     string `json:"display_key"`
	Thumbnail      *struct {
		URL         string `json:"url,omitempty"`
		ObjectKey   string `json:"object_key,omitempty"`
		ContentType string `json:"content_type,omitempty"`
		Status      string `json:"status,omitempty"`
	} `json:"thumbnail,omitempty"`
}

type fileRecord struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Note           string    `json:"note,omitempty"`
	UploadKey      string    `json:"upload_key"`
	OriginalName   string    `json:"original_name"`
	ContentType    string    `json:"content_type,omitempty"`
	SizeBytes      int64     `json:"size_bytes,omitempty"`
	ChecksumSHA256 string    `json:"checksum_sha256,omitempty"`
	ObjectKey      string    `json:"object_key"`
	DisplayKey     string    `json:"display_key"`
	ThumbnailURL   string    `json:"thumbnail_url,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type submitRequest struct {
	Title string     `json:"title"`
	Note  string     `json:"note,omitempty"`
	Files []fileFact `json:"files"`
}

type store struct {
	path    string
	mu      sync.Mutex
	records []fileRecord
}

type app struct {
	cfg   appConfig
	store *store
}

func main() {
	cfg := loadConfig()
	st, err := newStore(cfg.DataPath)
	if err != nil {
		log.Fatal(err)
	}
	a := &app{cfg: cfg, store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /demo/api/config", a.configAPI)
	mux.HandleFunc("GET /demo/api/files", a.listFilesAPI)
	mux.HandleFunc("POST /demo/api/files", a.createFilesAPI)
	mux.HandleFunc("DELETE /demo/api/files/{id}", a.deleteFileAPI)
	mux.HandleFunc("GET /demo/api/files/{id}/download", a.downloadFileAPI)
	mux.HandleFunc("GET /demo/api/files/download.zip", a.downloadZipAPI)
	mux.HandleFunc("GET /api/config", a.configAPI)
	mux.HandleFunc("/api/upload/", a.uploadProxy)
	mux.HandleFunc("/api/upload", a.uploadProxy)
	mux.HandleFunc("GET /api/files", a.listFilesAPI)
	mux.HandleFunc("POST /api/files", a.createFilesAPI)
	mux.HandleFunc("DELETE /api/files/{id}", a.deleteFileAPI)
	mux.HandleFunc("GET /api/files/{id}/download", a.downloadFileAPI)
	mux.HandleFunc("GET /api/files/download.zip", a.downloadZipAPI)
	log.Printf("demo app listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, mux))
}

func (a *app) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTemplate.Execute(w, map[string]string{"UploadBasePath": a.cfg.UploadBasePath})
}

func (a *app) configAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"upload_base_path":          a.cfg.UploadBasePath,
		"download_mode":             a.cfg.DownloadMode,
		"streamuploader_public_url": a.cfg.StreamUploaderPublicURL,
	})
}

func (a *app) uploadProxy(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(strings.TrimRight(a.cfg.StreamUploaderProxyURL, "/"))
	if err != nil || target.Scheme == "" || target.Host == "" {
		writeError(w, http.StatusBadGateway, "upload_proxy_not_configured", "streamuploader proxy URL is invalid")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = r.URL.Path
		req.URL.RawPath = r.URL.RawPath
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = target.Host
	}
	proxy.ServeHTTP(w, r)
}

func (a *app) listFilesAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.store.list())
}

func (a *app) createFilesAPI(w http.ResponseWriter, r *http.Request) {
	var req submitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "Untitled"
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "missing_files", "files is required")
		return
	}
	now := time.Now().UTC()
	records := make([]fileRecord, 0, len(req.Files))
	for _, file := range req.Files {
		if file.ObjectKey == "" {
			writeError(w, http.StatusBadRequest, "missing_object_key", "each file needs object_key")
			return
		}
		name := file.OriginalName
		if name == "" {
			name = filepath.Base(file.ObjectKey)
		}
		records = append(records, fileRecord{
			ID:             "file_" + token(12),
			Title:          req.Title,
			Note:           req.Note,
			UploadKey:      file.UploadKey,
			OriginalName:   name,
			ContentType:    file.ContentType,
			SizeBytes:      file.SizeBytes,
			ChecksumSHA256: file.ChecksumSHA256,
			ObjectKey:      file.ObjectKey,
			DisplayKey:     file.DisplayKey,
			ThumbnailURL:   thumbnailURL(a.cfg, file),
			CreatedAt:      now,
		})
	}
	if err := a.store.add(records); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, records)
}

func (a *app) deleteFileAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, ok := a.store.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if a.cfg.DeleteObjectsOnDelete {
		if err := a.deleteObjectFromStreamUploader(r.Context(), rec.ObjectKey); err != nil {
			writeError(w, http.StatusBadGateway, "streamuploader_delete_error", err.Error())
			return
		}
	}
	if err := a.store.delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) downloadFileAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, ok := a.store.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	target, err := a.downloadURL(r.Context(), rec, r.URL.Query().Get("mode"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "download_url_error", err.Error())
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (a *app) downloadZipAPI(w http.ResponseWriter, r *http.Request) {
	ids := splitCSV(r.URL.Query().Get("ids"))
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "missing_ids", "ids is required")
		return
	}
	records := a.store.getMany(ids)
	if len(records) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "files not found")
		return
	}
	keys := make([]string, 0, len(records))
	for _, rec := range records {
		keys = append(keys, url.PathEscape(rec.ObjectKey))
	}
	archive := streamUploaderPublicURL(a.cfg) + "/api/files/" + strings.Join(keys, ",") + "?filename=" + url.QueryEscape("streamuploader-demo.zip")
	http.Redirect(w, r, archive, http.StatusFound)
}

func (a *app) downloadURL(ctx context.Context, rec fileRecord, mode string) (string, error) {
	if mode == "" {
		mode = a.cfg.DownloadMode
	}
	switch mode {
	case "direct", "s3_direct", "direct_public_bucket":
		return publicObjectURL(a.cfg, rec.ObjectKey), nil
	case "presigned", "s3_presigned", "s3_presigned_download":
		var out struct {
			URL string `json:"url"`
		}
		if err := a.postBackendJSON(ctx, "/internal/file/presigned-url", map[string]any{
			"object_key":  rec.ObjectKey,
			"file_name":   rec.OriginalName,
			"ttl_seconds": int(a.cfg.PresignTTL.Seconds()),
		}, &out); err != nil {
			return "", err
		}
		return out.URL, nil
	case "proxy", "api_proxy", "api_proxy_download":
		return streamUploaderPublicURL(a.cfg) + "/api/file/" + url.PathEscape(rec.ObjectKey) + "/download", nil
	case "shared", "shared_key", "shared_key_proxy_download":
		var out struct {
			DownloadURL string `json:"download_url"`
		}
		if err := a.postBackendJSON(ctx, "/internal/file/shared-keys", map[string]any{
			"object_key":   rec.ObjectKey,
			"file_name":    rec.OriginalName,
			"content_type": rec.ContentType,
			"created_by":   "demo-app",
			"ttl_seconds":  int((24 * time.Hour).Seconds()),
		}, &out); err != nil {
			return "", err
		}
		return out.DownloadURL, nil
	default:
		return "", fmt.Errorf("unsupported download mode %q", mode)
	}
}

func (a *app) postBackendJSON(ctx context.Context, path string, body any, out any) error {
	if strings.TrimSpace(a.cfg.BackendControlURL) == "" {
		return errors.New("SU_BACKEND_CONTROL_URL is required")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.BackendControlURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.BackendAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.BackendAuthToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("backend returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func newStore(path string) (*store, error) {
	st := &store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		st.records = []fileRecord{}
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		st.records = []fileRecord{}
		return st, nil
	}
	if err := json.Unmarshal(body, &st.records); err != nil {
		return nil, err
	}
	return st, nil
}

func (a *app) deleteObjectFromStreamUploader(ctx context.Context, objectKey string) error {
	if strings.TrimSpace(a.cfg.BackendControlURL) == "" {
		return errors.New("SU_BACKEND_CONTROL_URL is required when SU_DELETE_OBJECTS_ON_DELETE is true")
	}
	endpoint := strings.TrimRight(a.cfg.BackendControlURL, "/") + "/internal/objects/" + url.PathEscape(objectKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	if a.cfg.BackendAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.BackendAuthToken)
	}
	req.Header.Set("X-Request-ID", "demo-delete-"+token(8))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("backend control delete returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func (s *store) list() []fileRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]fileRecord(nil), s.records...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *store) get(id string) (fileRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.records {
		if rec.ID == id {
			return rec, true
		}
	}
	return fileRecord{}, false
}

func (s *store) getMany(ids []string) []fileRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]struct{}{}
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make([]fileRecord, 0, len(want))
	for _, rec := range s.records {
		if _, ok := want[rec.ID]; ok {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (s *store) add(records []fileRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
	return s.saveLocked()
}

func (s *store) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.records[:0]
	for _, rec := range s.records {
		if rec.ID != id {
			next = append(next, rec)
		}
	}
	s.records = next
	return s.saveLocked()
}

func (s *store) saveLocked() error {
	body, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func loadConfig() appConfig {
	return appConfig{
		Addr:                    env("ADDR", ":8081"),
		DataPath:                env("DEMO_DATA_PATH", "/data/files.json"),
		UploadBasePath:          cleanBasePath(env("UPLOAD_BASE_PATH", "/api/upload")),
		DownloadMode:            env("DOWNLOAD_MODE", "presigned"),
		S3Bucket:                env("S3_BUCKET", "stream-upload"),
		S3DownloadEndpoint:      env("S3_DOWNLOAD_ENDPOINT", env("S3_PUBLIC_ENDPOINT", "http://localhost:9000")),
		S3ForcePathStyle:        envBool("S3_FORCE_PATH_STYLE", true),
		PresignTTL:              envDuration("PRESIGN_TTL", 15*time.Minute),
		DeleteObjectsOnDelete:   envBool("DELETE_OBJECTS_ON_DELETE", false),
		StreamUploaderPublicURL: env("STREAMUPLOADER_PUBLIC_URL", "http://localhost:8080"),
		StreamUploaderProxyURL:  env("STREAMUPLOADER_PROXY_URL", env("STREAMUPLOADER_PUBLIC_URL", "http://localhost:8080")),
		BackendControlURL:       env("BACKEND_CONTROL_URL", ""),
		BackendAuthToken:        env("BACKEND_AUTH_TOKEN", ""),
	}
}

func publicObjectURL(cfg appConfig, objectKey string) string {
	base := strings.TrimRight(cfg.S3DownloadEndpoint, "/")
	if cfg.S3ForcePathStyle {
		return base + "/" + url.PathEscape(cfg.S3Bucket) + "/" + escapeObjectPath(objectKey)
	}
	return base + "/" + escapeObjectPath(objectKey)
}

func thumbnailURL(cfg appConfig, file fileFact) string {
	if file.Thumbnail != nil && file.Thumbnail.URL != "" {
		return file.Thumbnail.URL
	}
	if file.ObjectKey == "" {
		return ""
	}
	return streamUploaderPublicURL(cfg) + "/api/file/" + url.PathEscape(file.ObjectKey) + "/thumbnail"
}

func streamUploaderPublicURL(cfg appConfig) string {
	return strings.TrimRight(cfg.StreamUploaderPublicURL, "/")
}

func escapeObjectPath(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func env(key, fallback string) string {
	if value := os.Getenv("SU_" + key); value != "" {
		return value
	}
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func cleanBasePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "/api/upload"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return strings.TrimRight(value, "/")
}

func sanitizeHeaderValue(value string) string {
	return strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(value)
}

func token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "=")
}

var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Stream Uploader Demo</title>
  <style>
    :root { color-scheme: light; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: #f6f7f9; color: #16181d; }
    main { max-width: 1080px; margin: 0 auto; padding: 28px 18px 48px; }
    h1 { font-size: 28px; margin: 0 0 20px; }
    h2 { font-size: 18px; margin: 0 0 14px; }
    section { background: #fff; border: 1px solid #d9dde5; border-radius: 8px; padding: 18px; margin: 0 0 18px; }
    label { display: block; font-size: 13px; font-weight: 650; margin: 12px 0 6px; }
    input[type="text"], textarea { width: 100%; box-sizing: border-box; border: 1px solid #c6ccd7; border-radius: 6px; padding: 9px 10px; font: inherit; }
    textarea { min-height: 72px; resize: vertical; }
    input[type="file"] { display: block; margin-top: 8px; }
    button, .button { border: 1px solid #1f5eff; background: #1f5eff; color: #fff; border-radius: 6px; padding: 8px 12px; font: inherit; cursor: pointer; text-decoration: none; display: inline-block; }
    button.secondary { background: #fff; color: #1f2937; border-color: #b8bfcc; }
    button.danger { background: #b42318; border-color: #b42318; }
    button:disabled { opacity: .55; cursor: not-allowed; }
    .tabs { display: flex; gap: 8px; margin: 0 0 18px; flex-wrap: wrap; }
    .tab-button { background: #fff; color: #1f2937; border-color: #b8bfcc; }
    .tab-button.active { background: #1f5eff; color: #fff; border-color: #1f5eff; }
    .tab-panel { display: none; }
    .tab-panel.active { display: block; }
    table { width: 100%; border-collapse: collapse; font-size: 14px; }
    th, td { border-bottom: 1px solid #e3e7ee; padding: 9px 7px; text-align: left; vertical-align: top; }
    th { color: #475467; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
    .row-actions { display: flex; gap: 8px; flex-wrap: wrap; }
    .thumb { width: 72px; height: 72px; object-fit: contain; background: #eef1f5; border: 1px solid #d9dde5; border-radius: 6px; display: block; }
    .thumb-missing { width: 72px; height: 72px; background: #eef1f5; border: 1px dashed #c6ccd7; border-radius: 6px; }
    .toolbar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
    .muted { color: #667085; font-size: 13px; }
    .uploads { display: grid; gap: 8px; margin-top: 12px; }
    .upload-item { border: 1px solid #e3e7ee; border-radius: 6px; padding: 10px; background: #fafbfc; }
    .bar { height: 8px; background: #e4e7ec; border-radius: 999px; overflow: hidden; margin-top: 8px; }
    .bar > span { display: block; height: 100%; background: #16a34a; width: 0%; transition: width .15s ease; }
    .topline { display: flex; justify-content: space-between; gap: 12px; align-items: center; }
    .status { font-size: 12px; color: #475467; }
    .invalid-results { display: grid; gap: 10px; margin-top: 14px; }
    .invalid-result { border: 1px solid #e3e7ee; background: #fafbfc; border-radius: 6px; padding: 10px; }
    .toasts { position: fixed; right: 18px; bottom: 18px; display: grid; gap: 10px; width: min(420px, calc(100vw - 36px)); z-index: 20; }
    .toast { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px; align-items: start; border-radius: 6px; padding: 12px 14px; background: #111827; color: #fff; box-shadow: 0 14px 34px rgba(15,23,42,.22); font-size: 14px; }
    .toast.error { background: #b42318; }
    .toast-message { white-space: pre-wrap; word-break: break-word; }
    .toast-copy { min-height: 28px; padding: 4px 8px; border: 1px solid rgba(255,255,255,.5); border-radius: 6px; background: rgba(255,255,255,.12); color: #fff; font-size: 12px; }
    .toast-copy:hover { background: rgba(255,255,255,.2); }
    pre { white-space: pre-wrap; word-break: break-word; margin: 8px 0 0; font-size: 12px; background: #111827; color: #f9fafb; border-radius: 6px; padding: 10px; }
  </style>
</head>
<body>
<main>
  <h1>Stream Uploader Demo</h1>
  <nav class="tabs" aria-label="Demo pages">
    <button class="tab-button active" data-tab-target="upload">Upload Files</button>
    <button class="tab-button" data-tab-target="invalid">Upload Invalid Files</button>
  </nav>
  <div class="tab-panel active" id="tab-upload">
    <section>
      <h2>Upload Files</h2>
      <label for="title">Title</label>
      <input id="title" type="text" placeholder="Release assets">
      <label for="note">Note</label>
      <textarea id="note" placeholder="Optional description"></textarea>
      <label for="files">Files</label>
      <input id="files" type="file" multiple>
      <div class="uploads" id="uploads"></div>
      <p class="muted">Files upload through streamuploader. The file list demonstrates direct, presigned, proxy, shared-key, and zip downloads.</p>
      <button id="save" disabled>Save File List</button>
    </section>
    <section>
      <div class="topline">
        <h2>Files</h2>
        <div class="toolbar">
          <button class="secondary" id="zipSelected" disabled>Download ZIP</button>
          <button class="secondary" id="refresh">Refresh</button>
        </div>
      </div>
      <table>
        <thead><tr><th><input type="checkbox" id="selectAll" aria-label="Select all files"></th><th>Thumbnail</th><th>Title</th><th>File</th><th>Size</th><th>Object Key</th><th>Actions</th></tr></thead>
        <tbody id="fileRows"></tbody>
      </table>
    </section>
  </div>
  <section class="tab-panel" id="tab-invalid">
    <div class="topline">
      <h2>Upload Invalid Files</h2>
      <button id="runInvalidUploads">Run Invalid Uploads</button>
    </div>
    <div class="invalid-results" id="invalidResults"></div>
  </section>
</main>
<div class="toasts" id="toasts" aria-live="polite" aria-atomic="true"></div>
<script>
const uploadBase = "{{.UploadBasePath}}";
const uploadsEl = document.querySelector("#uploads");
const fileInput = document.querySelector("#files");
const saveButton = document.querySelector("#save");
const refreshButton = document.querySelector("#refresh");
const zipSelectedButton = document.querySelector("#zipSelected");
const selectAll = document.querySelector("#selectAll");
const rows = document.querySelector("#fileRows");
const invalidButton = document.querySelector("#runInvalidUploads");
const invalidResults = document.querySelector("#invalidResults");
const toasts = document.querySelector("#toasts");
const pending = new Map();
const selected = new Set();
const activeToastMessages = new Set();
let ws;

document.querySelectorAll(".tab-button").forEach(button => {
  button.addEventListener("click", () => {
    document.querySelectorAll(".tab-button").forEach(item => item.classList.toggle("active", item === button));
    document.querySelectorAll(".tab-panel").forEach(panel => panel.classList.toggle("active", panel.id === "tab-" + button.dataset.tabTarget));
  });
});

function connectWatch() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  ws = new WebSocket(proto + "//" + location.host + uploadBase + "/watch");
  ws.onopen = () => {
    const keys = [...pending.keys()];
    if (keys.length) ws.send(JSON.stringify({type: "watch", upload_keys: keys}));
  };
  ws.onmessage = event => {
    const msg = JSON.parse(event.data);
    if (!msg.upload_key || !pending.has(msg.upload_key)) return;
    const item = pending.get(msg.upload_key);
    if (msg.item) item.fact = msg.item;
    if (msg.status) item.status = msg.status;
    if (msg.error) item.error = msg.message || msg.error;
    if (msg.item && msg.item.error) item.error = msg.item.error;
    if (msg.uploaded_bytes) item.uploadedBytes = msg.uploaded_bytes;
    if (item.status === "failed" || item.status === "canceled" || item.status === "expired") {
      pending.delete(msg.upload_key);
      showToast(item.name + ": " + (item.error || item.status));
    }
    renderUploads();
    updateSaveState();
  };
  ws.onclose = () => setTimeout(connectWatch, 1000);
}

function watchKey(key) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({type: "watch", upload_keys: [key]}));
  }
}

fileInput.addEventListener("change", async () => {
  for (const file of Array.from(fileInput.files || [])) {
    await startUpload(file);
  }
});

async function startUpload(file) {
  let uploadKey = "";
  try {
    const keyResp = await fetch(uploadEndpoint("/keys"), {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({file_name: file.name, content_type: file.type, size_bytes: file.size})
    });
    const key = await readJSONOrText(keyResp);
    if (!keyResp.ok) throw new Error(formatResponseError(key));
    if (!key || typeof key.upload_key !== "string") throw new Error("upload key response is missing upload_key");
    uploadKey = key.upload_key;
    pending.set(uploadKey, {name: file.name, size: file.size, status: "key_created", uploadedBytes: 0, fact: key});
    renderUploads();
    watchKey(uploadKey);
    const putResp = await fetch(uploadContentEndpoint(key), {
      method: "PUT",
      headers: {"Content-Type": file.type || "application/octet-stream"},
      body: file
    });
    const uploaded = await readJSONOrText(putResp);
    if (!putResp.ok) throw new Error(formatResponseError(uploaded));
    const item = pending.get(uploadKey);
    if (!item || !uploaded || typeof uploaded !== "object") throw new Error("upload response is invalid");
    item.status = uploaded.status;
    item.fact = uploaded;
    item.uploadedBytes = uploaded.uploaded_bytes || uploaded.size_bytes || file.size;
    renderUploads();
    updateSaveState();
  } catch (err) {
    const message = file.name + ": " + errorMessage(err);
    if (!uploadKey || pending.delete(uploadKey)) showToast(message);
    renderUploads();
    updateSaveState();
  }
}

function renderUploads() {
  uploadsEl.innerHTML = "";
  for (const [key, item] of pending) {
    const pct = item.size ? Math.min(100, Math.round((item.uploadedBytes || 0) / item.size * 100)) : (item.status === "uploaded" ? 100 : 0);
    const div = document.createElement("div");
    div.className = "upload-item";
    div.innerHTML = "<div class='topline'><strong></strong><span class='status'></span></div><div class='bar'><span></span></div>";
    div.querySelector("strong").textContent = item.name;
    div.querySelector(".status").textContent = item.status + " " + pct + "%";
    div.querySelector(".bar span").style.width = pct + "%";
    uploadsEl.appendChild(div);
  }
}

function updateSaveState() {
  const items = [...pending.values()];
  saveButton.disabled = items.length === 0 || !items.every(item => item.status === "uploaded" && item.fact && item.fact.object_key);
}

saveButton.addEventListener("click", async () => {
  const files = [...pending.values()].filter(item => item.status === "uploaded" && item.fact && item.fact.object_key).map(item => item.fact);
  if (!files.length) return;
  saveButton.disabled = true;
  try {
    const resp = await fetch("/demo/api/files", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({title: document.querySelector("#title").value, note: document.querySelector("#note").value, files})
    });
    const body = await readJSONOrText(resp);
    if (!resp.ok) throw new Error(formatResponseError(body));
    pending.clear();
    fileInput.value = "";
    renderUploads();
    updateSaveState();
    await loadFiles();
  } catch (err) {
    showToast("Save failed: " + errorMessage(err));
    updateSaveState();
  }
});

invalidButton.addEventListener("click", async () => {
  invalidButton.disabled = true;
  invalidResults.innerHTML = "";
  const cases = [
    {
      label: "JPEG declaration with ELF bytes",
      fileName: "fake.jpg",
      contentType: "image/jpeg",
      body: new Uint8Array([0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00])
    },
    {
      label: "Plain text declaration with bash script",
      fileName: "note.txt",
      contentType: "text/plain",
      body: new TextEncoder().encode("#!/bin/bash\necho denied\n")
    },
    {
      label: "EICAR antivirus test file",
      fileName: "eicar.txt",
      contentType: "text/plain",
      body: eicarTestFileBody()
    }
  ];
  for (const item of cases) {
    await runInvalidUpload(item);
  }
  invalidButton.disabled = false;
});

function eicarTestFileBody() {
  const pieces = [
    "X5O", "!P%", "@AP", "[4\\", "PZX", "54(", "P^)", "7CC",
    ")7}", "$EI", "CAR", "-ST", "AND", "ARD", "-AN", "TIV",
    "IRU", "S-T", "EST", "-FI", "LE!", "$H+", "H*"
  ];
  return new TextEncoder().encode(pieces.join(""));
}

async function runInvalidUpload(item) {
  const result = document.createElement("div");
  result.className = "invalid-result";
  result.innerHTML = "<strong></strong><div class='status'></div><pre></pre>";
  result.querySelector("strong").textContent = item.label;
  result.querySelector(".status").textContent = "uploading";
  invalidResults.appendChild(result);
  try {
    const keyResp = await fetch(uploadEndpoint("/keys"), {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({file_name: item.fileName, content_type: item.contentType, size_bytes: item.body.byteLength})
    });
    const keyBody = await readJSONOrText(keyResp);
    if (!keyResp.ok) {
      result.querySelector(".status").textContent = "key failed " + keyResp.status;
      result.querySelector("pre").textContent = JSON.stringify(keyBody, null, 2);
      return;
    }
    if (!keyBody || typeof keyBody.upload_url !== "string") {
      result.querySelector(".status").textContent = "key response missing upload_url";
      result.querySelector("pre").textContent = typeof keyBody === "string" ? keyBody : JSON.stringify(keyBody, null, 2);
      return;
    }
    const putURL = uploadContentEndpoint(keyBody);
    const putResp = await fetch(putURL, {
      method: "PUT",
      headers: {"Content-Type": item.contentType},
      body: item.body
    });
    const putBody = await readJSONOrText(putResp);
    result.querySelector(".status").textContent = putResp.status + " " + putResp.statusText;
    result.querySelector("pre").textContent = typeof putBody === "string" ? putBody : JSON.stringify(putBody, null, 2);
  } catch (err) {
    result.querySelector(".status").textContent = "request failed";
    result.querySelector("pre").textContent = String(err);
  }
}

function uploadEndpoint(path) {
  return uploadBase + path;
}

function uploadContentEndpoint(key) {
  if (key && typeof key.upload_key === "string") {
    return uploadEndpoint("/keys/" + encodeURIComponent(key.upload_key) + "/content");
  }
  if (key && typeof key.upload_url === "string") {
    return sameOriginPath(key.upload_url);
  }
  return "";
}

function sameOriginPath(value) {
  try {
    const url = new URL(value, location.href);
    if (url.origin === location.origin) return url.pathname + url.search + url.hash;
    return url.href;
  } catch {
    return value;
  }
}

async function readJSONOrText(resp) {
  const text = await resp.text();
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function formatResponseError(value) {
  if (!value) return "request failed";
  if (typeof value === "string") return value.trim() || "request failed";
  if (value.message) return value.message;
  if (value.error) return value.error;
  return JSON.stringify(value);
}

function errorMessage(err) {
  if (!err) return "request failed";
  return err.message || String(err);
}

function showToast(message, kind = "error") {
  const text = String(message || "request failed");
  const key = kind + "\n" + text;
  if (activeToastMessages.has(key)) return;
  activeToastMessages.add(key);
  const toast = document.createElement("div");
  toast.className = "toast " + kind;
  const body = document.createElement("div");
  body.className = "toast-message";
  body.textContent = text;
  const copy = document.createElement("button");
  copy.type = "button";
  copy.className = "toast-copy";
  copy.textContent = "Copy";
  copy.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(text);
      copy.textContent = "Copied";
    } catch {
      copy.textContent = "Copy failed";
    }
    setTimeout(() => {
      copy.textContent = "Copy";
    }, 1400);
  });
  toast.append(body, copy);
  toasts.appendChild(toast);
  setTimeout(() => {
    activeToastMessages.delete(key);
    toast.remove();
  }, 14000);
}

refreshButton.addEventListener("click", loadFiles);
zipSelectedButton.addEventListener("click", () => {
  const ids = [...selected];
  if (!ids.length) return;
  location.href = "/demo/api/files/download.zip?ids=" + encodeURIComponent(ids.join(","));
});

selectAll.addEventListener("change", () => {
  document.querySelectorAll("input[data-file-id]").forEach(input => {
    input.checked = selectAll.checked;
    if (input.checked) selected.add(input.dataset.fileId);
    else selected.delete(input.dataset.fileId);
  });
  updateZipState();
});

async function loadFiles() {
  let files = [];
  try {
    const resp = await fetch("/demo/api/files");
    const body = await readJSONOrText(resp);
    if (!resp.ok) throw new Error(formatResponseError(body));
    files = Array.isArray(body) ? body : (body && Array.isArray(body.files) ? body.files : []);
  } catch (err) {
    showToast("Load files failed: " + errorMessage(err));
  }
  rows.innerHTML = "";
  selected.clear();
  selectAll.checked = false;
  for (const file of files) {
    const tr = document.createElement("tr");
    tr.innerHTML = "<td><input type='checkbox' data-file-id='' aria-label='Select file'></td><td></td><td></td><td></td><td></td><td></td><td><div class='row-actions'><a class='button'>Direct</a><a class='button'>Presigned</a><a class='button'>Proxy</a><a class='button'>Shared</a><button class='danger'>Delete</button></div></td>";
    const checkbox = tr.querySelector("input[data-file-id]");
    checkbox.dataset.fileId = file.id;
    checkbox.addEventListener("change", () => {
      if (checkbox.checked) selected.add(file.id);
      else selected.delete(file.id);
      updateZipState();
    });
    if (file.thumbnail_url) {
      const img = document.createElement("img");
      img.className = "thumb";
      img.alt = "";
      img.loading = "lazy";
      img.src = file.thumbnail_url;
      img.onerror = () => img.replaceWith(Object.assign(document.createElement("div"), {className: "thumb-missing"}));
      tr.children[1].appendChild(img);
    } else {
      tr.children[1].appendChild(Object.assign(document.createElement("div"), {className: "thumb-missing"}));
    }
    tr.children[2].textContent = file.title;
    tr.children[3].textContent = file.original_name;
    tr.children[4].textContent = formatBytes(file.size_bytes);
    tr.children[5].textContent = file.object_key;
    const links = tr.querySelectorAll("a");
    links[0].href = "/demo/api/files/" + file.id + "/download?mode=direct";
    links[1].href = "/demo/api/files/" + file.id + "/download?mode=presigned";
    links[2].href = "/api/file/" + encodeURIComponent(file.object_key) + "/download";
    links[3].href = "/demo/api/files/" + file.id + "/download?mode=shared";
    tr.querySelector("button").onclick = async () => {
      await fetch("/demo/api/files/" + file.id, {method: "DELETE"});
      await loadFiles();
    };
    rows.appendChild(tr);
  }
  updateZipState();
}

function updateZipState() {
  zipSelectedButton.disabled = selected.size === 0;
  const boxes = [...document.querySelectorAll("input[data-file-id]")];
  selectAll.checked = boxes.length > 0 && boxes.every(input => input.checked);
}

function formatBytes(n) {
  if (!n) return "-";
  const units = ["B","KB","MB","GB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(i ? 1 : 0) + " " + units[i];
}

connectWatch();
loadFiles();
</script>
</body>
</html>`))
