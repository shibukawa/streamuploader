package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"math"
	stdmime "mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gorilla/websocket"

	"streamuploader/internal/config"
	"streamuploader/internal/extraction"
	"streamuploader/internal/model"
	"streamuploader/internal/storage"
	"streamuploader/internal/thumbnail"
)

var safeSegmentPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type Server struct {
	cfg        config.Config
	store      storage.Store
	proxy      *httputil.ReverseProxy
	upgrader   websocket.Upgrader
	mu         sync.RWMutex
	uploads    map[string]*model.UploadItem
	watchers   map[string]map[chan model.WatchServerMessage]struct{}
	uploadBase string
	fileBase   string
	filesBase  string
}

type uploadDeadlineMarker struct {
	UploadKey            string `json:"upload_key"`
	ObjectKey            string `json:"object_key"`
	TempObjectKey        string `json:"temp_object_key,omitempty"`
	OwnerTokenHash       string `json:"owner_token_hash,omitempty"`
	UploadStartDeadline  string `json:"upload_start_deadline"`
	UploadFinishDeadline string `json:"upload_finish_deadline"`
	Status               string `json:"status"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

type asyncTaskMarker struct {
	ObjectKey string `json:"object_key"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func New(cfg config.Config, store storage.Store) *Server {
	if cfg.UploadBasePath == "" {
		cfg.UploadBasePath = "/api/upload"
	}
	if cfg.BackendBasePath == "" {
		cfg.BackendBasePath = "/internal"
	}
	if cfg.SharedKeyBits < 96 {
		cfg.SharedKeyBits = 96
	}
	if cfg.SharedKeyPrefix == "" {
		cfg.SharedKeyPrefix = ".streamuploader/shared/"
	}
	if cfg.MaxArchiveFiles <= 0 {
		cfg.MaxArchiveFiles = 100
	}
	if cfg.MaxArchiveBytes <= 0 {
		cfg.MaxArchiveBytes = 1 << 30
	}
	if cfg.MaxUploadKeysPerOwner <= 0 {
		cfg.MaxUploadKeysPerOwner = 1000
	}
	if cfg.Security.MimeMagic.PrefixBytes <= 0 {
		cfg.Security = config.DefaultSecurityPolicy()
	}
	if cfg.UploadDeadlines.MarkerPrefix == "" {
		cfg.UploadDeadlines = config.DefaultSecurityPolicy().UploadDeadlines
	}
	if cfg.HTTPCache.Mode == "" {
		cfg.HTTPCache = config.DefaultSecurityPolicy().HTTPCache
	}
	defaultTextExtraction := config.DefaultSecurityPolicy().TextExtraction
	if cfg.TextExtraction.ExecutionMode == "" {
		cfg.TextExtraction.ExecutionMode = defaultTextExtraction.ExecutionMode
	}
	if cfg.TextExtraction.ObjectKeySuffix == "" {
		cfg.TextExtraction.ObjectKeySuffix = defaultTextExtraction.ObjectKeySuffix
	}
	if cfg.TextExtraction.MaxInputBytes <= 0 {
		cfg.TextExtraction.MaxInputBytes = defaultTextExtraction.MaxInputBytes
	}
	if cfg.TextExtraction.MaxOutputBytes <= 0 {
		cfg.TextExtraction.MaxOutputBytes = defaultTextExtraction.MaxOutputBytes
	}
	if cfg.TextExtraction.ExternalTimeout <= 0 {
		cfg.TextExtraction.ExternalTimeout = defaultTextExtraction.ExternalTimeout
	}
	thumbnail.Configure(cfg.Thumbnails)
	extraction.Configure(cfg.TextExtraction)
	var proxy *httputil.ReverseProxy
	if cfg.ApplicationServerURL != "" {
		if target, err := url.Parse(cfg.ApplicationServerURL); err == nil {
			proxy = httputil.NewSingleHostReverseProxy(target)
			originalDirector := proxy.Director
			proxy.Director = func(r *http.Request) {
				originalDirector(r)
				r.Host = target.Host
			}
		}
	}
	checkOrigin := func(r *http.Request) bool {
		return originAllowed(r.Header.Get("Origin"), cfg.AllowedOrigins)
	}
	return &Server{
		cfg:        cfg,
		store:      store,
		proxy:      proxy,
		upgrader:   websocket.Upgrader{CheckOrigin: checkOrigin},
		uploads:    map[string]*model.UploadItem{},
		watchers:   map[string]map[chan model.WatchServerMessage]struct{}{},
		uploadBase: strings.TrimRight(cfg.UploadBasePath, "/"),
		fileBase:   "/api/file",
		filesBase:  "/api/files",
	}
}

func (s *Server) Handler() http.Handler {
	return s.withAccessLog(http.HandlerFunc(s.route))
}

func (s *Server) BackendHandler() http.Handler {
	return s.withAccessLog(http.HandlerFunc(s.backendRoute))
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.cors(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/healthz" {
		s.health(w)
		return
	}
	if s.cfg.BackendAddr == "" && (r.URL.Path == s.cfg.BackendBasePath || strings.HasPrefix(r.URL.Path, s.cfg.BackendBasePath+"/")) {
		s.handleBackendAPI(w, r)
		return
	}
	if r.URL.Path == s.uploadBase || strings.HasPrefix(r.URL.Path, s.uploadBase+"/") {
		s.handleUploadAPI(w, r)
		return
	}
	if r.URL.Path == s.fileBase || strings.HasPrefix(r.URL.Path, s.fileBase+"/") {
		s.handleFileAPI(w, r)
		return
	}
	if r.URL.Path == s.filesBase || strings.HasPrefix(r.URL.Path, s.filesBase+"/") {
		s.handleFilesAPI(w, r)
		return
	}
	if s.proxy != nil && s.cfg.Mode == "simple_fronting_reverse_proxy" {
		s.proxy.ServeHTTP(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func (s *Server) backendRoute(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		s.health(w)
		return
	}
	if r.URL.Path == s.cfg.BackendBasePath || strings.HasPrefix(r.URL.Path, s.cfg.BackendBasePath+"/") {
		s.handleBackendAPI(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "backend route not found")
}

func (s *Server) health(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUploadAPI(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, s.uploadBase)
	if rel == "" {
		rel = "/"
	}
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	switch {
	case rel == "/keys" && r.Method == http.MethodPost:
		s.createUploadKey(w, r)
	case rel == "/wait" && r.Method == http.MethodPost:
		s.waitUploads(w, r)
	case rel == "/watch" && r.Method == http.MethodGet:
		s.watchUploads(w, r)
	case len(parts) == 2 && parts[0] == "keys" && r.Method == http.MethodGet:
		s.getUpload(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "keys" && r.Method == http.MethodDelete:
		s.cancelUploadKey(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "keys" && parts[2] == "content" && r.Method == http.MethodPut:
		s.uploadFile(w, r, parts[1])
	default:
		writeError(w, http.StatusNotFound, "not_found", "upload route not found")
	}
}

func (s *Server) handleBackendAPI(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBackend(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "backend authorization failed")
		return
	}
	escapedPath := r.URL.EscapedPath()
	base := strings.TrimRight(s.cfg.BackendBasePath, "/")
	filePrefix := base + "/file/"
	if strings.HasPrefix(escapedPath, filePrefix) {
		s.handleBackendFileAPI(w, r, strings.TrimPrefix(escapedPath, filePrefix))
		return
	}
	objectsPrefix := base + "/objects/"
	if strings.HasPrefix(escapedPath, objectsPrefix) {
		if s.handleBackendObjectAPI(w, r, strings.TrimPrefix(escapedPath, objectsPrefix)) {
			return
		}
	}
	if strings.HasPrefix(escapedPath, objectsPrefix) && r.Method == http.MethodDelete {
		escapedKey := strings.TrimPrefix(escapedPath, objectsPrefix)
		if strings.Contains(escapedKey, "/") {
			writeError(w, http.StatusNotFound, "not_found", "backend route not found")
			return
		}
		objectKey, err := url.PathUnescape(escapedKey)
		if err != nil || strings.TrimSpace(objectKey) == "" {
			writeError(w, http.StatusBadRequest, "invalid_object_key", "object key is required")
			return
		}
		if err := s.deleteObjectAndShares(r.Context(), objectKey); err != nil {
			writeError(w, http.StatusBadGateway, "storage_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if escapedPath == base+"/tasks/wait" && r.Method == http.MethodGet {
		s.waitAsyncTasks(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "backend route not found")
}

type presignRequest struct {
	ObjectKey  string `json:"object_key"`
	FileName   string `json:"file_name,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type sharedKeyRequest struct {
	ObjectKey   string `json:"object_key"`
	FileName    string `json:"file_name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
}

type sharedKeyRecord struct {
	TargetObjectKey string `json:"target_object_key"`
	OriginalName    string `json:"original_name,omitempty"`
	ContentType     string `json:"content_type,omitempty"`
	CreatedAt       string `json:"created_at"`
	CreatedBy       string `json:"created_by,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	Revoked         bool   `json:"revoked,omitempty"`
}

type extractedContentResponse struct {
	ObjectKey         string                      `json:"object_key"`
	ArtifactObjectKey string                      `json:"artifact_object_key"`
	Status            string                      `json:"status"`
	Content           *extraction.Content         `json:"content,omitempty"`
	Tasks             []model.WaitAsyncTaskStatus `json:"tasks,omitempty"`
	ErrorCode         string                      `json:"error_code,omitempty"`
}

type extractedContentPresignRequest struct {
	TTLSeconds           int  `json:"ttl_seconds,omitempty"`
	Wait                 bool `json:"wait,omitempty"`
	IncludePendingStatus bool `json:"include_pending_status,omitempty"`
}

func (s *Server) handleBackendObjectAPI(w http.ResponseWriter, r *http.Request, escapedRel string) bool {
	const extractedSuffix = "/extracted-content"
	const extractedPresignSuffix = "/extracted-content/presigned-url"
	switch {
	case strings.HasSuffix(escapedRel, extractedPresignSuffix) && r.Method == http.MethodPost:
		s.createExtractedContentPresignedURL(w, r, strings.TrimSuffix(escapedRel, extractedPresignSuffix))
		return true
	case strings.HasSuffix(escapedRel, extractedSuffix) && r.Method == http.MethodGet:
		s.getExtractedContent(w, r, strings.TrimSuffix(escapedRel, extractedSuffix))
		return true
	default:
		return false
	}
}

func (s *Server) handleBackendFileAPI(w http.ResponseWriter, r *http.Request, escapedRel string) {
	switch {
	case escapedRel == "presigned-url" && r.Method == http.MethodPost:
		s.createPresignedURL(w, r)
	case escapedRel == "shared-keys" && r.Method == http.MethodPost:
		s.createSharedKey(w, r)
	case strings.HasPrefix(escapedRel, "shared-keys/") && r.Method == http.MethodDelete:
		sharedKey, err := url.PathUnescape(strings.TrimPrefix(escapedRel, "shared-keys/"))
		if err != nil || strings.TrimSpace(sharedKey) == "" || strings.Contains(sharedKey, "/") {
			writeError(w, http.StatusBadRequest, "invalid_shared_key", "shared key is invalid")
			return
		}
		if !s.cfg.EnableSharedKey {
			writeError(w, http.StatusNotFound, "not_found", "shared key API is disabled")
			return
		}
		if err := s.deleteSharedKey(r.Context(), sharedKey); err != nil {
			writeError(w, http.StatusBadGateway, "storage_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "not_found", "backend file route not found")
	}
}

func (s *Server) authorizeBackend(r *http.Request) bool {
	if s.cfg.BackendAuthToken == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	return auth == "Bearer "+s.cfg.BackendAuthToken
}

func (s *Server) createPresignedURL(w http.ResponseWriter, r *http.Request) {
	var req presignRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
		return
	}
	objectKey := strings.TrimSpace(req.ObjectKey)
	if objectKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_object_key", "object_key is required")
		return
	}
	ttl := s.cfg.PresignTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	name := req.FileName
	if name == "" {
		name = path.Base(objectKey)
	}
	out, err := s.store.PresignGetObject(r.Context(), storage.PresignGetInput{
		Bucket:                     s.cfg.Bucket,
		Key:                        objectKey,
		Expires:                    ttl,
		ResponseContentDisposition: contentDispositionAttachment(name),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "presign_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        out.URL,
		"expires_at": out.ExpiresAt,
	})
}

func (s *Server) getExtractedContent(w http.ResponseWriter, r *http.Request, escapedKey string) {
	objectKey, ok := backendObjectKeyFromEscaped(w, escapedKey)
	if !ok {
		return
	}
	query := r.URL.Query()
	kinds := []string{"text_extraction", "metadata_extraction", "ocr_extraction"}
	if queryBool(query.Get("wait")) {
		tasks, ready := s.waitForAsyncTaskKinds(r.Context(), []string{objectKey}, kinds, query.Get("timeout_seconds"), query.Get("poll_millis"))
		if !ready {
			writeJSON(w, http.StatusAccepted, extractedContentResponse{
				ObjectKey:         objectKey,
				ArtifactObjectKey: extraction.ArtifactObjectKey(objectKey, s.cfg.TextExtraction),
				Status:            "pending",
				Tasks:             tasks,
			})
			return
		}
	}
	tasks, ready := s.asyncTaskStatuses(r.Context(), []string{objectKey}, kinds)
	if !ready {
		writeJSON(w, http.StatusAccepted, extractedContentResponse{
			ObjectKey:         objectKey,
			ArtifactObjectKey: extraction.ArtifactObjectKey(objectKey, s.cfg.TextExtraction),
			Status:            "pending",
			Tasks:             tasks,
		})
		return
	}
	artifactKey := extraction.ArtifactObjectKey(objectKey, s.cfg.TextExtraction)
	out, err := s.store.GetObject(r.Context(), storage.GetInput{Bucket: s.cfg.Bucket, Key: artifactKey})
	if err != nil {
		status := "not_scheduled"
		if s.cfg.TextExtraction.Enabled {
			status = "skipped"
		}
		if queryBool(query.Get("status_only")) {
			writeJSON(w, http.StatusOK, extractedContentResponse{
				ObjectKey:         objectKey,
				ArtifactObjectKey: artifactKey,
				Status:            status,
				Tasks:             tasks,
			})
			return
		}
		writeJSON(w, http.StatusNotFound, extractedContentResponse{
			ObjectKey:         objectKey,
			ArtifactObjectKey: artifactKey,
			Status:            status,
			Tasks:             tasks,
			ErrorCode:         "artifact_not_found",
		})
		return
	}
	defer out.Body.Close()
	var content extraction.Content
	body, err := io.ReadAll(io.LimitReader(out.Body, s.cfg.TextExtraction.MaxOutputBytes+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	if s.cfg.TextExtraction.MaxOutputBytes > 0 && int64(len(body)) > s.cfg.TextExtraction.MaxOutputBytes {
		writeError(w, http.StatusBadGateway, "artifact_too_large", "extracted content artifact exceeds configured maximum")
		return
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &content); err != nil {
			writeError(w, http.StatusBadGateway, "invalid_artifact", "extracted content artifact is not valid JSON")
			return
		}
	}
	if include := includeSet(query["include"]); len(include) > 0 {
		content = extraction.Filter(content, include)
	}
	resp := extractedContentResponse{
		ObjectKey:         objectKey,
		ArtifactObjectKey: artifactKey,
		Status:            "generated",
		Tasks:             tasks,
	}
	if !queryBool(query.Get("status_only")) {
		resp.Content = &content
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) createExtractedContentPresignedURL(w http.ResponseWriter, r *http.Request, escapedKey string) {
	objectKey, ok := backendObjectKeyFromEscaped(w, escapedKey)
	if !ok {
		return
	}
	var req extractedContentPresignRequest
	if r.Body != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
			return
		}
	}
	kinds := []string{"text_extraction", "metadata_extraction", "ocr_extraction"}
	if req.Wait {
		tasks, ready := s.waitForAsyncTaskKinds(r.Context(), []string{objectKey}, kinds, "", "")
		if !ready {
			writeJSON(w, http.StatusAccepted, extractedContentResponse{
				ObjectKey:         objectKey,
				ArtifactObjectKey: extraction.ArtifactObjectKey(objectKey, s.cfg.TextExtraction),
				Status:            "pending",
				Tasks:             tasks,
			})
			return
		}
	}
	artifactKey := extraction.ArtifactObjectKey(objectKey, s.cfg.TextExtraction)
	if _, err := s.store.HeadObject(r.Context(), storage.HeadInput{Bucket: s.cfg.Bucket, Key: artifactKey}); err != nil {
		writeJSON(w, http.StatusNotFound, extractedContentResponse{
			ObjectKey:         objectKey,
			ArtifactObjectKey: artifactKey,
			Status:            "not_scheduled",
			ErrorCode:         "artifact_not_found",
		})
		return
	}
	ttl := s.cfg.PresignTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	out, err := s.store.PresignGetObject(r.Context(), storage.PresignGetInput{
		Bucket:  s.cfg.Bucket,
		Key:     artifactKey,
		Expires: ttl,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "presign_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_object_key": artifactKey,
		"url":                 out.URL,
		"expires_at":          out.ExpiresAt,
		"status":              "generated",
	})
}

func (s *Server) createSharedKey(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableSharedKey {
		writeError(w, http.StatusNotFound, "not_found", "shared key API is disabled")
		return
	}
	var req sharedKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
		return
	}
	objectKey := strings.TrimSpace(req.ObjectKey)
	if objectKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_object_key", "object_key is required")
		return
	}
	now := time.Now().UTC()
	expiresAt := ""
	if req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_expires_at", "expires_at must be RFC3339")
			return
		}
		if s.cfg.SharedKeyMaxTTL > 0 && parsed.UTC().After(now.Add(s.cfg.SharedKeyMaxTTL)) {
			writeError(w, http.StatusBadRequest, "ttl_too_large", "shared key expiry exceeds max ttl")
			return
		}
		expiresAt = parsed.UTC().Format(time.RFC3339)
	} else if req.TTLSeconds > 0 {
		ttl := time.Duration(req.TTLSeconds) * time.Second
		if s.cfg.SharedKeyMaxTTL > 0 && ttl > s.cfg.SharedKeyMaxTTL {
			writeError(w, http.StatusBadRequest, "ttl_too_large", "shared key ttl exceeds max ttl")
			return
		}
		expiresAt = now.Add(ttl).Format(time.RFC3339)
	} else if s.cfg.SharedKeyTTL > 0 {
		expiresAt = now.Add(s.cfg.SharedKeyTTL).Format(time.RFC3339)
	}
	sharedKey := randomToken(int(math.Ceil(float64(s.cfg.SharedKeyBits) / 8)))
	rec := sharedKeyRecord{
		TargetObjectKey: objectKey,
		OriginalName:    req.FileName,
		ContentType:     req.ContentType,
		CreatedAt:       now.Format(time.RFC3339),
		CreatedBy:       req.CreatedBy,
		ExpiresAt:       expiresAt,
	}
	body, _ := json.Marshal(rec)
	metadata := map[string]string{
		"target-object-key": objectKey,
		"created-at":        rec.CreatedAt,
	}
	if req.FileName != "" {
		metadata["original-name"] = req.FileName
	}
	if req.ContentType != "" {
		metadata["content-type"] = req.ContentType
	}
	if req.CreatedBy != "" {
		metadata["created-by"] = req.CreatedBy
	}
	if expiresAt != "" {
		metadata["expires-at"] = expiresAt
	}
	if _, err := s.store.PutObject(r.Context(), storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         s.sharedKeyObjectKey(sharedKey),
		Body:        bytes.NewReader(body),
		ContentType: "application/json",
		Metadata:    metadata,
	}); err != nil {
		writeError(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	if _, err := s.store.PutObject(r.Context(), storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         s.sharedKeyMarkerObjectKey(objectKey, sharedKey),
		Body:        bytes.NewReader(body),
		ContentType: "application/json",
		Metadata:    metadata,
	}); err != nil {
		_ = s.store.DeleteObject(r.Context(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: s.sharedKeyObjectKey(sharedKey)})
		writeError(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"shared_key":   sharedKey,
		"download_url": strings.TrimRight(s.cfg.PublicBaseURL, "/") + s.fileBase + "/shared/" + url.PathEscape(sharedKey) + "/download",
		"expires_at":   expiresAt,
	})
}

func (s *Server) handleFileAPI(w http.ResponseWriter, r *http.Request) {
	escaped := r.URL.EscapedPath()
	prefix := s.fileBase + "/"
	switch {
	case strings.HasPrefix(escaped, prefix+"shared/") && (strings.HasSuffix(escaped, "/content") || strings.HasSuffix(escaped, "/download")) && r.Method == http.MethodGet:
		attachment := strings.HasSuffix(escaped, "/download")
		suffix := "/content"
		if attachment {
			suffix = "/download"
		}
		sharedKey := strings.TrimSuffix(strings.TrimPrefix(escaped, prefix+"shared/"), suffix)
		if strings.Contains(sharedKey, "/") {
			writeError(w, http.StatusNotFound, "not_found", "file route not found")
			return
		}
		key, name, contentType, ok := s.resolveSharedKey(w, r, sharedKey)
		if !ok {
			return
		}
		s.streamObject(w, r, key, name, contentType, attachment)
	case strings.HasPrefix(escaped, prefix) && strings.HasSuffix(escaped, "/thumbnail") && r.Method == http.MethodGet:
		if !s.cfg.Thumbnails.Enabled {
			writeError(w, http.StatusNotFound, "not_found", "thumbnail generation is disabled")
			return
		}
		key, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(escaped, prefix), "/thumbnail"))
		if err != nil || strings.TrimSpace(key) == "" {
			writeError(w, http.StatusBadRequest, "invalid_object_key", "object key is invalid")
			return
		}
		s.streamObject(w, r, key+s.cfg.Thumbnails.ObjectKeySuffix, path.Base(key), "", false)
	case strings.HasPrefix(escaped, prefix) && (strings.HasSuffix(escaped, "/content") || strings.HasSuffix(escaped, "/download")) && r.Method == http.MethodGet:
		attachment := strings.HasSuffix(escaped, "/download")
		suffix := "/content"
		if attachment {
			suffix = "/download"
		}
		key, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(escaped, prefix), suffix))
		if err != nil || strings.TrimSpace(key) == "" {
			writeError(w, http.StatusBadRequest, "invalid_object_key", "object key is invalid")
			return
		}
		s.streamObject(w, r, key, path.Base(key), "", attachment)
	default:
		writeError(w, http.StatusNotFound, "not_found", "file route not found")
	}
}

func (s *Server) handleFilesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), s.filesBase+"/")
	if escaped == "" || strings.Contains(escaped, "/") {
		writeError(w, http.StatusNotFound, "not_found", "files route not found")
		return
	}
	parts := strings.Split(escaped, ",")
	if len(parts) == 0 || len(parts) > s.cfg.MaxArchiveFiles {
		writeError(w, http.StatusBadRequest, "too_many_files", "too many files requested")
		return
	}
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key, err := url.PathUnescape(part)
		if err != nil || strings.TrimSpace(key) == "" {
			writeError(w, http.StatusBadRequest, "invalid_object_key", "object key is invalid")
			return
		}
		keys = append(keys, key)
	}
	s.streamZipArchive(w, r, keys)
}

func (s *Server) createUploadKey(w http.ResponseWriter, r *http.Request) {
	var req model.CreateUploadKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
		return
	}
	if strings.TrimSpace(req.FileName) == "" {
		writeError(w, http.StatusBadRequest, "invalid_file_name", "file_name is required")
		return
	}
	maxUploadBytes := s.effectiveMaxUploadBytes()
	if req.SizeBytes > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "resource_limit_exceeded", "declared upload size exceeds configured maximum file size")
		return
	}
	now := time.Now().UTC()
	ownerToken := s.uploadOwnerToken(w, r)
	ownerTokenHash := hashToken(ownerToken)
	if s.activeUploadKeyCountForOwner(ownerTokenHash) >= s.cfg.MaxUploadKeysPerOwner {
		writeError(w, http.StatusTooManyRequests, "too_many_upload_keys", "too many active upload keys for owner")
		return
	}
	uploadKey := randomToken(24)
	fileName := safeSegment(req.FileName)
	prefix := storagePrefix(uploadKey, req.Prefix)
	objectKey := path.Join(prefix, fileName)
	expiresAt := now.Add(s.cfg.SessionTTL)
	if s.cfg.UploadDeadlines.Enabled {
		expiresAt = now.Add(s.cfg.UploadDeadlines.StartTimeout)
	}
	item := &model.UploadItem{
		UploadKey:      uploadKey,
		Role:           req.Role,
		OriginalName:   req.FileName,
		ContentType:    req.ContentType,
		SizeBytes:      req.SizeBytes,
		StoragePrefix:  prefix,
		ObjectKey:      objectKey,
		DisplayKey:     objectKey,
		Status:         model.UploadKeyCreated,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      expiresAt,
		OwnerTokenHash: ownerTokenHash,
	}
	if s.cfg.UploadDeadlines.Enabled {
		if err := s.putUploadMarker(r.Context(), uploadDeadlineMarker{
			UploadKey:            uploadKey,
			ObjectKey:            objectKey,
			OwnerTokenHash:       ownerTokenHash,
			UploadStartDeadline:  now.Add(s.cfg.UploadDeadlines.StartTimeout).Format(time.RFC3339Nano),
			UploadFinishDeadline: now.Add(s.cfg.UploadDeadlines.FinishTimeout).Format(time.RFC3339Nano),
			Status:               "key_created",
			CreatedAt:            now.Format(time.RFC3339Nano),
			UpdatedAt:            now.Format(time.RFC3339Nano),
		}); err != nil {
			writeError(w, http.StatusBadGateway, "storage_error", err.Error())
			return
		}
	}
	s.mu.Lock()
	s.uploads[uploadKey] = item
	s.mu.Unlock()
	s.broadcastSnapshot(item)
	writeJSON(w, http.StatusCreated, model.CreateUploadKeyResponse{
		UploadKey:      uploadKey,
		ExpiresAt:      item.ExpiresAt,
		UploadURL:      fmt.Sprintf("%s%s/keys/%s/content", strings.TrimRight(s.cfg.PublicBaseURL, "/"), s.uploadBase, uploadKey),
		StoragePrefix:  prefix,
		ObjectKey:      objectKey,
		DisplayKey:     objectKey,
		MaxUploadBytes: maxUploadBytes,
	})
}

func (s *Server) getUpload(w http.ResponseWriter, _ *http.Request, uploadKey string) {
	item, ok := s.upload(uploadKey)
	if !ok {
		writeError(w, http.StatusNotFound, "upload_not_found", "upload key not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) cancelUploadKey(w http.ResponseWriter, r *http.Request, uploadKey string) {
	item, ok := s.upload(uploadKey)
	if !ok {
		writeError(w, http.StatusNotFound, "upload_not_found", "upload key not found")
		return
	}
	if item.Status != model.UploadKeyCreated {
		writeError(w, http.StatusConflict, "upload_already_started", "upload key can be canceled only before upload starts")
		return
	}
	if !s.uploadOwnerMatches(r, item.OwnerTokenHash) {
		writeError(w, http.StatusForbidden, "owner_mismatch", "upload key belongs to another client")
		return
	}
	if s.cfg.UploadDeadlines.Enabled {
		marker, err := s.loadUploadMarker(r.Context(), uploadKey)
		if err != nil {
			writeError(w, http.StatusGone, "upload_key_expired", "upload key expired or missing")
			return
		}
		if marker.OwnerTokenHash != "" && !s.uploadOwnerMatches(r, marker.OwnerTokenHash) {
			writeError(w, http.StatusForbidden, "owner_mismatch", "upload key belongs to another client")
			return
		}
		_ = s.deleteUploadMarker(r.Context(), uploadKey)
	}
	s.updateUpload(uploadKey, func(current *model.UploadItem) {
		current.Status = model.UploadCanceled
		current.UpdatedAt = time.Now().UTC()
	})
	s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: model.UploadCanceled, Item: mustUpload(s.upload(uploadKey))})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) effectiveMaxUploadBytes() int64 {
	maxUploadBytes := s.cfg.MaxUploadBytes
	if s.cfg.Security.ResourceLimits.Enabled && s.cfg.Security.ResourceLimits.MaxFileSizeBytes > 0 && (maxUploadBytes <= 0 || s.cfg.Security.ResourceLimits.MaxFileSizeBytes < maxUploadBytes) {
		maxUploadBytes = s.cfg.Security.ResourceLimits.MaxFileSizeBytes
	}
	if maxUploadBytes <= 0 {
		return 1 << 30
	}
	return maxUploadBytes
}

func (s *Server) activeUploadKeyCountForOwner(ownerTokenHash string) int {
	if ownerTokenHash == "" {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, item := range s.uploads {
		if item == nil || item.OwnerTokenHash != ownerTokenHash {
			continue
		}
		switch item.Status {
		case model.UploadKeyCreated, model.UploadUploading:
			count++
		}
	}
	return count
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, uploadKey string) {
	target, ok := s.uploadTarget(uploadKey)
	if !ok {
		writeError(w, http.StatusNotFound, "upload_not_found", "upload key not found")
		return
	}
	if target.status == model.UploadUploaded {
		writeError(w, http.StatusConflict, "already_uploaded", "upload key already has content")
		return
	}
	if target.status == model.UploadCanceled {
		writeError(w, http.StatusGone, "upload_canceled", "upload key was canceled")
		return
	}
	uploadCtx := r.Context()
	var cancel context.CancelFunc
	var marker uploadDeadlineMarker
	if s.cfg.UploadDeadlines.Enabled {
		var err error
		marker, err = s.loadUploadMarker(r.Context(), uploadKey)
		if err != nil {
			writeError(w, http.StatusGone, "upload_key_expired", "upload key expired or missing")
			return
		}
		now := time.Now().UTC()
		startDeadline, err := time.Parse(time.RFC3339Nano, marker.UploadStartDeadline)
		if err != nil || now.After(startDeadline) {
			s.expireUpload(uploadKey, "upload key start deadline has passed")
			_ = s.deleteUploadMarker(r.Context(), uploadKey)
			writeError(w, http.StatusGone, "upload_key_expired", "upload key start deadline has passed")
			return
		}
		finishDeadline, err := time.Parse(time.RFC3339Nano, marker.UploadFinishDeadline)
		if err != nil {
			finishDeadline = now.Add(s.cfg.UploadDeadlines.FinishTimeout)
		}
		uploadCtx, cancel = context.WithDeadline(r.Context(), finishDeadline)
		defer cancel()
	}
	contentType := target.contentType
	if contentType == "" {
		contentType = r.Header.Get("Content-Type")
	}
	s.updateUpload(uploadKey, func(item *model.UploadItem) {
		item.Status = model.UploadUploading
		item.ContentType = contentType
		item.UpdatedAt = time.Now().UTC()
	})

	maxUploadBytes := s.effectiveMaxUploadBytes()
	if r.ContentLength > maxUploadBytes {
		err := securityUploadError{
			status:  http.StatusRequestEntityTooLarge,
			code:    "resource_limit_exceeded",
			message: "uploaded file exceeds configured maximum file size",
		}
		s.failUpload(uploadKey, err.Error())
		writeError(w, err.status, err.code, err.Error())
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxUploadBytes)
	body := io.Reader(limited)
	var inspectedPrefix []byte
	archiveKind := archiveKindFor(contentType, target.originalName)
	inspection, err := inspectUploadPrefix(limited, contentType, target.originalName, s.cfg.Security.MimeMagic)
	if err != nil {
		s.failUpload(uploadKey, err.Error())
		status, code := securityErrorResponse(err)
		writeError(w, status, code, err.Error())
		return
	}
	inspectedPrefix = inspection.prefix
	if archiveKind == archiveUnknown {
		archiveKind = archiveKindFromMagic(inspectedPrefix)
	}
	body = io.MultiReader(bytes.NewReader(inspection.prefix), limited)
	if inspection.detectedContentType != "" && contentType == "" {
		contentType = inspection.detectedContentType
		s.updateUpload(uploadKey, func(item *model.UploadItem) {
			item.ContentType = contentType
			item.UpdatedAt = time.Now().UTC()
		})
	}
	sanitized, err := applyFileSanitization(body, contentType, target.originalName, s.cfg.Security)
	if err != nil {
		s.failUpload(uploadKey, err.Error())
		status, code := securityErrorResponse(err)
		writeError(w, status, code, err.Error())
		return
	}
	body = sanitized.reader
	if sanitized.contentType != "" {
		contentType = sanitized.contentType
	}
	documentFullScanUpload := fileRequiresPostUploadFullScan(contentType, target.originalName, s.cfg.Security)
	archiveUpload := s.cfg.Security.ArchiveGuard.Enabled && archiveKind != archiveUnknown
	securityStagedUpload := archiveUpload || s.cfg.Security.ClamAV.Enabled || documentFullScanUpload
	objectKey := target.objectKey
	if securityStagedUpload {
		objectKey = temporaryUploadObjectKey(target.objectKey, uploadKey)
	}
	if s.cfg.UploadDeadlines.Enabled {
		marker.Status = "uploading"
		marker.TempObjectKey = ""
		if securityStagedUpload {
			marker.TempObjectKey = objectKey
		}
		marker.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		_ = s.putUploadMarker(r.Context(), marker)
	}
	measured := &progressReader{
		reader: body,
		hash:   sha256.New(),
		notify: func(n int64) {
			displayedBytes := displayedUploadBytes(n, target.sizeBytes, securityStagedUpload)
			s.updateUpload(uploadKey, func(item *model.UploadItem) {
				item.UploadedBytes = displayedBytes
				item.UpdatedAt = time.Now().UTC()
			})
			s.broadcast(model.WatchServerMessage{
				Type:          "progress",
				UploadKey:     uploadKey,
				UploadedBytes: displayedBytes,
				SizeBytes:     target.sizeBytes,
				Status:        model.UploadUploading,
			})
		},
	}
	var thumbnailJob *thumbnailUploadJob
	if s.thumbnailEligible(contentType) && !documentFullScanUpload {
		thumbnailJob = s.startThumbnailUpload(target.objectKey, contentType)
	}
	var sideWriters []*io.PipeWriter
	var documentFullScanDone <-chan error
	if thumbnailJob != nil {
		sideWriters = append(sideWriters, thumbnailJob.writer)
	}
	if documentFullScanUpload {
		documentWriter, done := startDocumentFullScan(contentType, target.originalName, s.cfg.Security)
		documentFullScanDone = done
		sideWriters = append(sideWriters, documentWriter)
	}
	result, err := s.putObjectWithSecurityScan(uploadCtx, storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         objectKey,
		ContentType: contentType,
	}, measured, sideWriters...)
	if err != nil {
		if securityStagedUpload {
			_ = s.store.DeleteObject(context.Background(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
		}
		code := "storage_error"
		status := http.StatusBadGateway
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			code = "upload_too_large"
			status = http.StatusRequestEntityTooLarge
		} else if securityStatus, securityCode := securityErrorResponse(err); securityCode != "content_rejected" {
			code = securityCode
			status = securityStatus
		} else if errors.Is(uploadCtx.Err(), context.DeadlineExceeded) {
			code = "upload_deadline_exceeded"
			status = http.StatusRequestTimeout
		}
		s.updateUpload(uploadKey, func(item *model.UploadItem) {
			item.Status = model.UploadFailed
			item.Error = err.Error()
			item.UpdatedAt = time.Now().UTC()
		})
		s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: model.UploadFailed, Item: mustUpload(s.upload(uploadKey))})
		writeError(w, status, code, err.Error())
		return
	}
	if archiveUpload {
		if err := inspectArchiveObject(uploadCtx, s.store, s.cfg.Bucket, objectKey, measured.n, archiveKind, target.originalName, s.cfg.Security); err != nil {
			_ = s.store.DeleteObject(context.Background(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
			s.failUpload(uploadKey, err.Error())
			status, code := securityErrorResponse(err)
			writeError(w, status, code, err.Error())
			return
		}
	}
	if documentFullScanUpload {
		if err := <-documentFullScanDone; err != nil {
			_ = s.store.DeleteObject(context.Background(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
			s.failUpload(uploadKey, err.Error())
			status, code := securityErrorResponse(err)
			writeError(w, status, code, err.Error())
			return
		}
	}
	if securityStagedUpload {
		copyResult, err := s.store.CopyObject(uploadCtx, storage.CopyInput{
			Bucket:      s.cfg.Bucket,
			SourceKey:   objectKey,
			Key:         target.objectKey,
			ContentType: contentType,
		})
		_ = s.store.DeleteObject(context.Background(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
		if err != nil {
			s.failUpload(uploadKey, err.Error())
			writeError(w, http.StatusBadGateway, "storage_error", err.Error())
			return
		}
		if copyResult.ETag != "" {
			result.ETag = copyResult.ETag
		}
	}
	if s.cfg.UploadDeadlines.Enabled {
		_ = s.deleteUploadMarker(context.Background(), uploadKey)
	}
	now := time.Now().UTC()
	var uploaded *model.UploadItem
	s.updateUpload(uploadKey, func(item *model.UploadItem) {
		item.Status = model.UploadUploaded
		item.ContentType = contentType
		item.UploadedBytes = measured.n
		item.SizeBytes = measured.n
		item.ChecksumSHA256 = hex.EncodeToString(measured.hash.Sum(nil))
		item.UploadedAt = &now
		item.UpdatedAt = now
		if s.thumbnailEligible(contentType) {
			item.Thumbnail = &model.DerivedAsset{
				Kind:      "image_thumbnail",
				ObjectKey: item.ObjectKey + s.cfg.Thumbnails.ObjectKeySuffix,
				URL:       s.thumbnailURL(item.ObjectKey),
				Status:    "pending",
			}
		}
		if extraction.ShouldSchedule(contentType, s.cfg.TextExtraction) {
			item.ExtractedContent = &model.DerivedAsset{
				Kind:        "extracted_content",
				ObjectKey:   extraction.ArtifactObjectKey(item.ObjectKey, s.cfg.TextExtraction),
				ContentType: "application/json; charset=utf-8",
				Status:      "pending",
			}
		}
		uploaded = cloneUpload(item)
	})
	if s.thumbnailEligible(contentType) {
		if s.cfg.Thumbnails.ExecutionMode == "sequential" {
			uploaded = s.finishThumbnailUpload(uploadKey, thumbnailJob, uploadCtx, contentType)
		} else {
			if err := s.putAsyncTaskMarker(context.Background(), asyncTaskMarker{
				ObjectKey: target.objectKey,
				Kind:      "image_thumbnail",
				Status:    "running",
			}); err != nil {
				slog.Warn("async_task_marker_create_failed", "upload_key", uploadKey, "object_key", target.objectKey, "kind", "image_thumbnail", "error", err)
			}
			go s.finishThumbnailUpload(uploadKey, thumbnailJob, context.Background(), contentType)
		}
	}
	if extraction.ShouldSchedule(contentType, s.cfg.TextExtraction) {
		if s.cfg.TextExtraction.ExecutionMode == "sequential" {
			uploaded = s.finishTextExtraction(uploadKey, uploadCtx, contentType)
		} else {
			for _, kind := range extraction.TaskKindsForContentType(contentType, s.cfg.TextExtraction) {
				if err := s.putAsyncTaskMarker(context.Background(), asyncTaskMarker{
					ObjectKey: target.objectKey,
					Kind:      kind,
					Status:    "running",
				}); err != nil {
					slog.Warn("async_task_marker_create_failed", "upload_key", uploadKey, "object_key", target.objectKey, "kind", kind, "error", err)
				}
			}
			go s.finishTextExtraction(uploadKey, context.Background(), contentType)
		}
	}
	w.Header().Set("ETag", result.ETag)
	s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: model.UploadUploaded, Item: uploaded})
	writeJSON(w, http.StatusOK, uploaded)
}

func (s *Server) failUpload(uploadKey, message string) {
	s.updateUpload(uploadKey, func(item *model.UploadItem) {
		item.Status = model.UploadFailed
		item.Error = message
		item.UpdatedAt = time.Now().UTC()
	})
	s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: model.UploadFailed, Item: mustUpload(s.upload(uploadKey))})
}

func (s *Server) expireUpload(uploadKey, message string) {
	s.updateUpload(uploadKey, func(item *model.UploadItem) {
		item.Status = model.UploadExpired
		item.Error = message
		item.UpdatedAt = time.Now().UTC()
	})
	s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: model.UploadExpired, Item: mustUpload(s.upload(uploadKey))})
}

func (s *Server) waitUploads(w http.ResponseWriter, r *http.Request) {
	var req model.WaitUploadsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON")
		return
	}
	if len(req.UploadKeys) == 0 {
		writeError(w, http.StatusBadRequest, "missing_upload_keys", "upload_keys is required")
		return
	}
	timeout := 60 * time.Second
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, ready := s.uploadsForKeys(req.UploadKeys)
		if ready {
			writeJSON(w, http.StatusOK, model.WaitUploadsResponse{Ready: true, Items: items})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			items, _ = s.uploadsForKeys(req.UploadKeys)
			writeJSON(w, http.StatusOK, model.WaitUploadsResponse{Ready: false, Timeout: true, Items: items})
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) waitAsyncTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	objectKeys := splitQueryValues(query["object_key"])
	objectKeys = append(objectKeys, splitQueryValues(query["object_keys"])...)
	if len(objectKeys) == 0 {
		writeError(w, http.StatusBadRequest, "missing_object_keys", "object_keys is required")
		return
	}
	kinds := splitQueryValues(query["kind"])
	kinds = append(kinds, splitQueryValues(query["kinds"])...)
	kinds = normalizedTaskKinds(kinds)
	timeout := 60 * time.Second
	if timeoutSeconds := positiveQueryInt(query.Get("timeout_seconds")); timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	poll := 200 * time.Millisecond
	if pollMillis := positiveQueryInt(query.Get("poll_millis")); pollMillis > 0 {
		poll = time.Duration(pollMillis) * time.Millisecond
	}
	if poll < 50*time.Millisecond {
		poll = 50 * time.Millisecond
	}
	if poll > 5*time.Second {
		poll = 5 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		tasks, ready := s.asyncTaskStatuses(r.Context(), objectKeys, kinds)
		if ready {
			writeJSON(w, http.StatusOK, model.WaitAsyncTasksResponse{Ready: true, Tasks: tasks})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			tasks, _ = s.asyncTaskStatuses(context.Background(), objectKeys, kinds)
			writeJSON(w, http.StatusOK, model.WaitAsyncTasksResponse{Ready: false, Timeout: true, Tasks: tasks})
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) waitForAsyncTaskKinds(ctx context.Context, objectKeys, kinds []string, timeoutValue, pollValue string) ([]model.WaitAsyncTaskStatus, bool) {
	timeout := 60 * time.Second
	if timeoutSeconds := positiveQueryInt(timeoutValue); timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	poll := 200 * time.Millisecond
	if pollMillis := positiveQueryInt(pollValue); pollMillis > 0 {
		poll = time.Duration(pollMillis) * time.Millisecond
	}
	if poll < 50*time.Millisecond {
		poll = 50 * time.Millisecond
	}
	if poll > 5*time.Second {
		poll = 5 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		tasks, ready := s.asyncTaskStatuses(ctx, objectKeys, kinds)
		if ready {
			return tasks, true
		}
		select {
		case <-ctx.Done():
			return tasks, false
		case <-deadline.C:
			tasks, _ = s.asyncTaskStatuses(context.Background(), objectKeys, kinds)
			return tasks, false
		case <-ticker.C:
		}
	}
}

func backendObjectKeyFromEscaped(w http.ResponseWriter, escapedKey string) (string, bool) {
	if strings.Contains(escapedKey, "/") {
		writeError(w, http.StatusNotFound, "not_found", "backend route not found")
		return "", false
	}
	objectKey, err := url.PathUnescape(escapedKey)
	if err != nil || strings.TrimSpace(objectKey) == "" {
		writeError(w, http.StatusBadRequest, "invalid_object_key", "object key is required")
		return "", false
	}
	return objectKey, true
}

func (s *Server) watchUploads(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	updates := make(chan model.WatchServerMessage, 64)
	watched := map[string]struct{}{}
	done := make(chan struct{})
	defer close(done)
	defer func() {
		s.mu.Lock()
		for key := range watched {
			s.removeWatcherLocked(key, updates)
		}
		s.mu.Unlock()
	}()
	go func() {
		for {
			select {
			case msg := <-updates:
				_ = conn.WriteJSON(msg)
			case <-done:
				return
			}
		}
	}()
	for {
		var msg model.WatchClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "watch":
			for _, key := range msg.UploadKeys {
				if key == "" {
					continue
				}
				s.mu.Lock()
				if _, ok := watched[key]; !ok {
					watched[key] = struct{}{}
					if s.watchers[key] == nil {
						s.watchers[key] = map[chan model.WatchServerMessage]struct{}{}
					}
					s.watchers[key][updates] = struct{}{}
				}
				item := cloneUpload(s.uploads[key])
				s.mu.Unlock()
				if item == nil {
					updates <- model.WatchServerMessage{Type: "error", UploadKey: key, Code: "upload_not_found", Message: "upload key not found"}
				} else {
					updates <- model.WatchServerMessage{Type: "snapshot", UploadKey: key, Item: item, Status: item.Status}
				}
			}
		case "unwatch":
			s.mu.Lock()
			for _, key := range msg.UploadKeys {
				delete(watched, key)
				s.removeWatcherLocked(key, updates)
			}
			s.mu.Unlock()
		case "ping":
			updates <- model.WatchServerMessage{Type: "snapshot", Message: "pong"}
		default:
			updates <- model.WatchServerMessage{Type: "error", Code: "unknown_message", Message: "unknown message type"}
		}
	}
}

func (s *Server) streamObject(w http.ResponseWriter, r *http.Request, objectKey, fileName, overrideContentType string, attachment bool) {
	if !s.cfg.AllowFrontendFileAccess {
		writeError(w, http.StatusForbidden, "file_access_disabled", "frontend file access is disabled")
		return
	}
	out, err := s.store.GetObject(r.Context(), storage.GetInput{
		Bucket: s.cfg.Bucket,
		Key:    objectKey,
		Range:  r.Header.Get("Range"),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	defer out.Body.Close()
	contentType := out.ContentType
	if overrideContentType != "" {
		contentType = overrideContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if fileName == "" {
		fileName = path.Base(objectKey)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.applyCacheHeaders(w)
	if attachment {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(fileName))
	}
	if out.ETag != "" && s.cfg.HTTPCache.ForwardETag {
		w.Header().Set("ETag", out.ETag)
	}
	if !out.LastModified.IsZero() && s.cfg.HTTPCache.ForwardLastMod {
		w.Header().Set("Last-Modified", out.LastModified.UTC().Format(http.TimeFormat))
	}
	if out.ContentRange != "" {
		w.Header().Set("Content-Range", out.ContentRange)
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if out.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", out.ContentLength))
	}
	status := http.StatusOK
	if out.ContentRange != "" {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, out.Body)
}

func (s *Server) streamZipArchive(w http.ResponseWriter, r *http.Request, keys []string) {
	if !s.cfg.AllowFrontendFileAccess {
		writeError(w, http.StatusForbidden, "file_access_disabled", "frontend file access is disabled")
		return
	}
	type zipObject struct {
		key  string
		head storage.HeadResult
	}
	total := int64(0)
	objects := make([]zipObject, 0, len(keys))
	for _, key := range keys {
		head, err := s.store.HeadObject(r.Context(), storage.HeadInput{Bucket: s.cfg.Bucket, Key: key})
		if err != nil {
			writeError(w, http.StatusBadGateway, "storage_error", err.Error())
			return
		}
		if head.ContentLength > 0 {
			total += head.ContentLength
		}
		if total > s.cfg.MaxArchiveBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "archive_too_large", "archive request exceeds max archive bytes")
			return
		}
		objects = append(objects, zipObject{key: key, head: head})
	}
	name := safeSegment(r.URL.Query().Get("filename"))
	if name == "file" || !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.applyCacheHeaders(w)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(name))
	zw := zip.NewWriter(w)
	defer zw.Close()
	usedNames := map[string]int{}
	for _, obj := range objects {
		out, err := s.store.GetObject(r.Context(), storage.GetInput{Bucket: s.cfg.Bucket, Key: obj.key})
		if err != nil {
			return
		}
		entryName := uniqueArchiveName(archiveEntryName(obj.key, obj.head.Metadata), usedNames)
		header := &zip.FileHeader{
			Name:   entryName,
			Method: zip.Deflate,
		}
		header.SetModTime(time.Now())
		writer, err := zw.CreateHeader(header)
		if err != nil {
			_ = out.Body.Close()
			return
		}
		_, copyErr := io.Copy(writer, out.Body)
		_ = out.Body.Close()
		if copyErr != nil {
			return
		}
	}
}

func (s *Server) resolveSharedKey(w http.ResponseWriter, r *http.Request, escapedSharedKey string) (string, string, string, bool) {
	if !s.cfg.EnableSharedKey {
		writeError(w, http.StatusNotFound, "not_found", "shared key API is disabled")
		return "", "", "", false
	}
	sharedKey, err := url.PathUnescape(escapedSharedKey)
	if err != nil || strings.TrimSpace(sharedKey) == "" {
		writeError(w, http.StatusBadRequest, "invalid_shared_key", "shared key is invalid")
		return "", "", "", false
	}
	out, err := s.store.GetObject(r.Context(), storage.GetInput{Bucket: s.cfg.Bucket, Key: s.sharedKeyObjectKey(sharedKey)})
	if err != nil {
		writeError(w, http.StatusNotFound, "shared_key_not_found", "shared key not found")
		return "", "", "", false
	}
	defer out.Body.Close()
	var rec sharedKeyRecord
	body, _ := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	_ = json.Unmarshal(body, &rec)
	metadata := lowerMetadata(out.Metadata)
	if rec.TargetObjectKey == "" {
		rec.TargetObjectKey = metadata["target-object-key"]
	}
	if rec.OriginalName == "" {
		rec.OriginalName = metadata["original-name"]
	}
	if rec.ContentType == "" {
		rec.ContentType = metadata["content-type"]
	}
	if rec.ExpiresAt == "" {
		rec.ExpiresAt = metadata["expires-at"]
	}
	if rec.Revoked || strings.EqualFold(metadata["revoked"], "true") {
		writeError(w, http.StatusForbidden, "shared_key_revoked", "shared key was revoked")
		return "", "", "", false
	}
	if rec.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, rec.ExpiresAt)
		if err != nil || time.Now().UTC().After(expiresAt) {
			writeError(w, http.StatusForbidden, "shared_key_expired", "shared key expired")
			return "", "", "", false
		}
	}
	if rec.TargetObjectKey == "" {
		writeError(w, http.StatusBadGateway, "invalid_shared_key_record", "shared key record has no target object")
		return "", "", "", false
	}
	return rec.TargetObjectKey, rec.OriginalName, rec.ContentType, true
}

func (s *Server) sharedKeyObjectKey(sharedKey string) string {
	return path.Join(s.cfg.SharedKeyPrefix, sharedKey)
}

func (s *Server) sharedKeyMarkerPrefix(objectKey string) string {
	return path.Join(path.Dir(objectKey), ".shared") + "/"
}

func (s *Server) sharedKeyMarkerObjectKey(objectKey, sharedKey string) string {
	return path.Join(path.Dir(objectKey), ".shared", sharedKey)
}

func (s *Server) deleteSharedKey(ctx context.Context, sharedKey string) error {
	rec, ok, err := s.loadSharedKeyRecord(ctx, sharedKey)
	if err != nil {
		return err
	}
	if ok && rec.TargetObjectKey != "" {
		if err := s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: s.sharedKeyMarkerObjectKey(rec.TargetObjectKey, sharedKey)}); err != nil {
			return err
		}
	}
	return s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: s.sharedKeyObjectKey(sharedKey)})
}

func (s *Server) deleteObjectAndShares(ctx context.Context, objectKey string) error {
	if s.cfg.EnableSharedKey {
		if err := s.deleteSharesForObject(ctx, objectKey); err != nil {
			return err
		}
	}
	return s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
}

func (s *Server) deleteSharesForObject(ctx context.Context, objectKey string) error {
	markers, err := s.store.ListObjects(ctx, storage.ListInput{Bucket: s.cfg.Bucket, Prefix: s.sharedKeyMarkerPrefix(objectKey)})
	if err != nil {
		return err
	}
	for _, markerKey := range markers.Keys {
		sharedKey := path.Base(markerKey)
		rec, ok, err := s.loadSharedMarkerRecord(ctx, markerKey)
		if err != nil {
			return err
		}
		if ok && rec.TargetObjectKey != "" && rec.TargetObjectKey != objectKey {
			continue
		}
		if err := s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: markerKey}); err != nil {
			return err
		}
		if err := s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: s.sharedKeyObjectKey(sharedKey)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) uploadMarkerObjectKey(uploadKey string) string {
	return path.Join(s.cfg.UploadDeadlines.MarkerPrefix, uploadKey)
}

func (s *Server) putUploadMarker(ctx context.Context, marker uploadDeadlineMarker) error {
	body, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	_, err = s.store.PutObject(ctx, storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         s.uploadMarkerObjectKey(marker.UploadKey),
		Body:        bytes.NewReader(body),
		ContentType: "application/json",
		Metadata: map[string]string{
			"upload-key":             marker.UploadKey,
			"object-key":             marker.ObjectKey,
			"temp-object-key":        marker.TempObjectKey,
			"owner-token-hash":       marker.OwnerTokenHash,
			"upload-start-deadline":  marker.UploadStartDeadline,
			"upload-finish-deadline": marker.UploadFinishDeadline,
			"status":                 marker.Status,
		},
	})
	return err
}

func (s *Server) loadUploadMarker(ctx context.Context, uploadKey string) (uploadDeadlineMarker, error) {
	out, err := s.store.GetObject(ctx, storage.GetInput{Bucket: s.cfg.Bucket, Key: s.uploadMarkerObjectKey(uploadKey)})
	if err != nil {
		return uploadDeadlineMarker{}, err
	}
	defer out.Body.Close()
	var marker uploadDeadlineMarker
	body, _ := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	_ = json.Unmarshal(body, &marker)
	metadata := lowerMetadata(out.Metadata)
	if marker.UploadKey == "" {
		marker.UploadKey = metadata["upload-key"]
	}
	if marker.ObjectKey == "" {
		marker.ObjectKey = metadata["object-key"]
	}
	if marker.TempObjectKey == "" {
		marker.TempObjectKey = metadata["temp-object-key"]
	}
	if marker.OwnerTokenHash == "" {
		marker.OwnerTokenHash = metadata["owner-token-hash"]
	}
	if marker.UploadStartDeadline == "" {
		marker.UploadStartDeadline = metadata["upload-start-deadline"]
	}
	if marker.UploadFinishDeadline == "" {
		marker.UploadFinishDeadline = metadata["upload-finish-deadline"]
	}
	if marker.Status == "" {
		marker.Status = metadata["status"]
	}
	if marker.UploadKey == "" {
		marker.UploadKey = uploadKey
	}
	return marker, nil
}

func (s *Server) deleteUploadMarker(ctx context.Context, uploadKey string) error {
	return s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: s.uploadMarkerObjectKey(uploadKey)})
}

func (s *Server) asyncTaskObjectKey(objectKey, kind string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + objectKey))
	return path.Join(".streamuploader/tasks", hex.EncodeToString(sum[:])+".json")
}

func (s *Server) putAsyncTaskMarker(ctx context.Context, marker asyncTaskMarker) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if marker.CreatedAt == "" {
		marker.CreatedAt = now
	}
	marker.UpdatedAt = now
	body, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	_, err = s.store.PutObject(ctx, storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         s.asyncTaskObjectKey(marker.ObjectKey, marker.Kind),
		Body:        bytes.NewReader(body),
		ContentType: "application/json",
		Metadata: map[string]string{
			"object-key": marker.ObjectKey,
			"kind":       marker.Kind,
			"status":     marker.Status,
		},
	})
	return err
}

func (s *Server) deleteAsyncTaskMarker(ctx context.Context, objectKey, kind string) {
	_ = s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: s.asyncTaskObjectKey(objectKey, kind)})
}

func (s *Server) asyncTaskStatuses(ctx context.Context, objectKeys, kinds []string) ([]model.WaitAsyncTaskStatus, bool) {
	tasks := make([]model.WaitAsyncTaskStatus, 0, len(objectKeys)*len(kinds))
	ready := true
	for _, objectKey := range objectKeys {
		objectKey = strings.TrimSpace(objectKey)
		if objectKey == "" {
			continue
		}
		for _, kind := range kinds {
			pending := false
			if _, err := s.store.HeadObject(ctx, storage.HeadInput{Bucket: s.cfg.Bucket, Key: s.asyncTaskObjectKey(objectKey, kind)}); err == nil {
				pending = true
				ready = false
			}
			tasks = append(tasks, model.WaitAsyncTaskStatus{ObjectKey: objectKey, Kind: kind, Pending: pending})
		}
	}
	return tasks, ready
}

func (s *Server) CleanupExpiredUploads(ctx context.Context) error {
	if !s.cfg.UploadDeadlines.Enabled {
		return nil
	}
	list, err := s.store.ListObjects(ctx, storage.ListInput{Bucket: s.cfg.Bucket, Prefix: s.cfg.UploadDeadlines.MarkerPrefix})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, key := range list.Keys {
		uploadKey := path.Base(key)
		marker, err := s.loadUploadMarker(ctx, uploadKey)
		if err != nil {
			continue
		}
		startExpired := deadlineExpired(marker.UploadStartDeadline, now)
		finishExpired := deadlineExpired(marker.UploadFinishDeadline, now)
		if !startExpired && !finishExpired {
			continue
		}
		if marker.TempObjectKey != "" {
			_ = s.store.DeleteObject(ctx, storage.DeleteInput{Bucket: s.cfg.Bucket, Key: marker.TempObjectKey})
		}
		_ = s.deleteUploadMarker(ctx, uploadKey)
		s.expireUpload(uploadKey, "upload deadline expired")
		slog.Info("cleanup_deleted", "upload_key", uploadKey, "temp_object_key", marker.TempObjectKey)
	}
	return nil
}

func deadlineExpired(value string, now time.Time) bool {
	if value == "" {
		return false
	}
	deadline, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && now.After(deadline)
}

func (s *Server) loadSharedKeyRecord(ctx context.Context, sharedKey string) (sharedKeyRecord, bool, error) {
	return s.loadSharedRecordObject(ctx, s.sharedKeyObjectKey(sharedKey))
}

func (s *Server) loadSharedMarkerRecord(ctx context.Context, markerKey string) (sharedKeyRecord, bool, error) {
	return s.loadSharedRecordObject(ctx, markerKey)
}

func (s *Server) loadSharedRecordObject(ctx context.Context, objectKey string) (sharedKeyRecord, bool, error) {
	out, err := s.store.GetObject(ctx, storage.GetInput{Bucket: s.cfg.Bucket, Key: objectKey})
	if err != nil {
		return sharedKeyRecord{}, false, nil
	}
	defer out.Body.Close()
	var rec sharedKeyRecord
	body, _ := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	_ = json.Unmarshal(body, &rec)
	metadata := lowerMetadata(out.Metadata)
	if rec.TargetObjectKey == "" {
		rec.TargetObjectKey = metadata["target-object-key"]
	}
	if rec.OriginalName == "" {
		rec.OriginalName = metadata["original-name"]
	}
	if rec.ContentType == "" {
		rec.ContentType = metadata["content-type"]
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = metadata["created-at"]
	}
	if rec.CreatedBy == "" {
		rec.CreatedBy = metadata["created-by"]
	}
	if rec.ExpiresAt == "" {
		rec.ExpiresAt = metadata["expires-at"]
	}
	return rec, true, nil
}

func (s *Server) upload(key string) (*model.UploadItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item := s.uploads[key]
	return cloneUpload(item), item != nil
}

type uploadTarget struct {
	objectKey    string
	originalName string
	contentType  string
	sizeBytes    int64
	status       model.UploadStatus
}

func (s *Server) uploadTarget(uploadKey string) (uploadTarget, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item := s.uploads[uploadKey]
	if item == nil {
		return uploadTarget{}, false
	}
	return uploadTarget{
		objectKey:    item.ObjectKey,
		originalName: item.OriginalName,
		contentType:  item.ContentType,
		sizeBytes:    item.SizeBytes,
		status:       item.Status,
	}, true
}

func (s *Server) updateUpload(uploadKey string, fn func(*model.UploadItem)) {
	s.mu.Lock()
	item := s.uploads[uploadKey]
	if item != nil {
		fn(item)
	}
	s.mu.Unlock()
}

func (s *Server) uploadsForKeys(keys []string) ([]*model.UploadItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]*model.UploadItem, 0, len(keys))
	ready := true
	for _, key := range keys {
		item := cloneUpload(s.uploads[key])
		if item == nil {
			item = &model.UploadItem{UploadKey: key, Status: model.UploadFailed, Error: "upload key not found"}
		}
		if !terminal(item.Status) {
			ready = false
		}
		if s.cfg.Thumbnails.Enabled && s.cfg.Thumbnails.ExecutionMode == "sequential" && item.Thumbnail != nil && item.Thumbnail.Status == "pending" {
			ready = false
		}
		if s.cfg.TextExtraction.Enabled && s.cfg.TextExtraction.ExecutionMode == "sequential" && item.ExtractedContent != nil && item.ExtractedContent.Status == "pending" {
			ready = false
		}
		items = append(items, item)
	}
	return items, ready
}

func (s *Server) thumbnailEligible(contentType string) bool {
	if !s.cfg.Thumbnails.Enabled {
		return false
	}
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "image/jpeg", "image/pjpeg", "image/png", "image/gif", "image/webp", "image/avif",
		"image/tiff", "image/x-tiff", "image/bmp", "image/svg+xml",
		"image/heif", "image/heic", "image/heif-sequence", "image/heic-sequence",
		"image/jxl", "image/jp2", "image/jpx", "image/jpm", "image/jpf",
		"image/vnd.adobe.photoshop", "image/x-photoshop", "image/x-tga", "image/tga",
		"application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"video/mp4", "video/mpeg", "video/quicktime", "video/webm", "video/x-msvideo", "video/x-matroska":
		return true
	default:
		return false
	}
}

func (s *Server) thumbnailURL(objectKey string) string {
	return strings.TrimRight(s.cfg.PublicBaseURL, "/") + s.fileBase + "/" + url.PathEscape(objectKey) + "/thumbnail"
}

type thumbnailUploadJob struct {
	writer *io.PipeWriter
	done   chan thumbnailUploadResult
}

type thumbnailUploadResult struct {
	conversion thumbnail.Conversion
	err        error
}

func (s *Server) startThumbnailUpload(sourceKey, sourceContentType string) *thumbnailUploadJob {
	reader, writer := io.Pipe()
	job := &thumbnailUploadJob{
		writer: writer,
		done:   make(chan thumbnailUploadResult, 1),
	}
	go func() {
		conversion, err := thumbnail.ConvertFromReaderWithContentType(context.Background(), reader, sourceKey, sourceContentType, s.cfg.Thumbnails)
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
		job.done <- thumbnailUploadResult{conversion: conversion, err: err}
	}()
	return job
}

func (s *Server) finishThumbnailUpload(uploadKey string, job *thumbnailUploadJob, ctx context.Context, sourceContentType string) *model.UploadItem {
	item, _ := s.upload(uploadKey)
	if item != nil && s.cfg.Thumbnails.ExecutionMode == "async" {
		defer s.deleteAsyncTaskMarker(context.Background(), item.ObjectKey, "image_thumbnail")
	}
	if job == nil {
		return s.generateThumbnailForUpload(uploadKey, sourceContentType)
	}
	result := <-job.done
	if result.err == nil {
		result.err = thumbnail.StoreConversion(ctx, s.store, s.cfg.Bucket, result.conversion)
	}
	return s.updateThumbnailForUpload(uploadKey, result.conversion.Result, result.err)
}

func (s *Server) generateThumbnailForUpload(uploadKey, sourceContentType string) *model.UploadItem {
	item, ok := s.upload(uploadKey)
	if !ok || item.Thumbnail == nil {
		return item
	}
	result, err := thumbnail.GenerateWithContentType(context.Background(), s.store, s.cfg.Bucket, item.ObjectKey, sourceContentType, s.cfg.Thumbnails)
	return s.updateThumbnailForUpload(uploadKey, result, err)
}

func (s *Server) updateThumbnailForUpload(uploadKey string, result thumbnail.Result, err error) *model.UploadItem {
	item, ok := s.upload(uploadKey)
	if !ok {
		return nil
	}
	var updated *model.UploadItem
	s.updateUpload(uploadKey, func(current *model.UploadItem) {
		if current.Thumbnail == nil {
			current.Thumbnail = &model.DerivedAsset{Kind: "image_thumbnail", ObjectKey: current.ObjectKey + s.cfg.Thumbnails.ObjectKeySuffix, URL: s.thumbnailURL(current.ObjectKey)}
		}
		current.UpdatedAt = time.Now().UTC()
		if err != nil {
			current.Thumbnail.Status = "failed"
			current.Thumbnail.Error = err.Error()
			updated = cloneUpload(current)
			return
		}
		current.Thumbnail.Status = "generated"
		current.Thumbnail.ObjectKey = result.ObjectKey
		current.Thumbnail.URL = s.thumbnailURL(current.ObjectKey)
		current.Thumbnail.ContentType = result.ContentType
		current.Thumbnail.Width = result.Width
		current.Thumbnail.Height = result.Height
		current.Thumbnail.SizeBytes = result.SizeBytes
		updated = cloneUpload(current)
	})
	if err != nil {
		slog.Warn("thumbnail_generation_failed", "upload_key", uploadKey, "object_key", item.ObjectKey, "error", err)
	} else {
		slog.Info("thumbnail_generated", "upload_key", uploadKey, "object_key", result.ObjectKey, "content_type", result.ContentType, "backend", result.Backend)
	}
	if updated != nil {
		s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: updated.Status, Item: updated})
	}
	return updated
}

func (s *Server) finishTextExtraction(uploadKey string, ctx context.Context, sourceContentType string) *model.UploadItem {
	item, ok := s.upload(uploadKey)
	if !ok || item == nil {
		return item
	}
	kinds := extraction.TaskKindsForContentType(sourceContentType, s.cfg.TextExtraction)
	if s.cfg.TextExtraction.ExecutionMode == "async" {
		defer func() {
			for _, kind := range kinds {
				s.deleteAsyncTaskMarker(context.Background(), item.ObjectKey, kind)
			}
		}()
	}
	out, err := s.store.GetObject(ctx, storage.GetInput{Bucket: s.cfg.Bucket, Key: item.ObjectKey})
	if err != nil {
		return s.updateTextExtractionForUpload(uploadKey, extraction.Result{Status: "failed", ErrorCode: "source_read_failed"}, err)
	}
	defer out.Body.Close()
	result, err := extraction.Generate(ctx, item.ObjectKey, sourceContentType, out.Body, s.cfg.TextExtraction)
	if err == nil && result.Status == "generated" {
		body, marshalErr := extraction.Marshal(result.Content, s.cfg.TextExtraction)
		if marshalErr != nil {
			err = marshalErr
			result.Status = "failed"
			result.ErrorCode = "output_too_large"
		} else {
			artifactKey := extraction.ArtifactObjectKey(item.ObjectKey, s.cfg.TextExtraction)
			_, err = s.store.PutObject(ctx, storage.PutInput{
				Bucket:      s.cfg.Bucket,
				Key:         artifactKey,
				Body:        bytes.NewReader(body),
				ContentType: "application/json; charset=utf-8",
				Metadata: map[string]string{
					"source-object-key": item.ObjectKey,
					"kind":              "extracted_content",
				},
			})
			if err == nil {
				result.ObjectKey = artifactKey
				result.SizeBytes = int64(len(body))
			}
		}
	}
	return s.updateTextExtractionForUpload(uploadKey, result, err)
}

func (s *Server) updateTextExtractionForUpload(uploadKey string, result extraction.Result, err error) *model.UploadItem {
	item, ok := s.upload(uploadKey)
	if !ok {
		return nil
	}
	var updated *model.UploadItem
	s.updateUpload(uploadKey, func(current *model.UploadItem) {
		if current.ExtractedContent == nil {
			current.ExtractedContent = &model.DerivedAsset{
				Kind:        "extracted_content",
				ObjectKey:   extraction.ArtifactObjectKey(current.ObjectKey, s.cfg.TextExtraction),
				ContentType: "application/json; charset=utf-8",
			}
		}
		current.UpdatedAt = time.Now().UTC()
		current.ExtractedContent.ObjectKey = extraction.ArtifactObjectKey(current.ObjectKey, s.cfg.TextExtraction)
		current.ExtractedContent.ContentType = "application/json; charset=utf-8"
		current.ExtractedContent.SizeBytes = result.SizeBytes
		if err != nil {
			current.ExtractedContent.Status = "failed"
			current.ExtractedContent.Error = err.Error()
			updated = cloneUpload(current)
			return
		}
		current.ExtractedContent.Status = result.Status
		current.ExtractedContent.Error = ""
		updated = cloneUpload(current)
	})
	if err != nil {
		slog.Warn("text_extraction_failed", "upload_key", uploadKey, "object_key", item.ObjectKey, "error", err)
	} else {
		slog.Info("text_extraction_finished", "upload_key", uploadKey, "object_key", item.ObjectKey, "status", result.Status)
	}
	if updated != nil {
		s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: updated.Status, Item: updated})
	}
	return updated
}

func (s *Server) broadcastSnapshot(item *model.UploadItem) {
	s.broadcast(model.WatchServerMessage{Type: "snapshot", UploadKey: item.UploadKey, Item: cloneUpload(item), Status: item.Status})
}

func (s *Server) broadcast(msg model.WatchServerMessage) {
	if msg.UploadKey == "" {
		return
	}
	s.mu.RLock()
	watchers := make([]chan model.WatchServerMessage, 0, len(s.watchers[msg.UploadKey]))
	for ch := range s.watchers[msg.UploadKey] {
		watchers = append(watchers, ch)
	}
	s.mu.RUnlock()
	for _, ch := range watchers {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) removeWatcherLocked(key string, ch chan model.WatchServerMessage) {
	if s.watchers[key] == nil {
		return
	}
	delete(s.watchers[key], ch)
	if len(s.watchers[key]) == 0 {
		delete(s.watchers, key)
	}
}

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			slog.Info("http_request",
				"method", r.Method,
				"route", routeTemplate(r.URL.Path, s),
				"status", http.StatusSwitchingProtocols,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", r.Header.Get("X-Request-ID"),
				"source_ip", r.RemoteAddr,
			)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http_request",
			"method", r.Method,
			"route", routeTemplate(r.URL.Path, s),
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", r.Header.Get("X-Request-ID"),
			"source_ip", r.RemoteAddr,
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) cors(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode != "standalone_cross_origin" && s.cfg.Mode != "standalone" {
		return
	}
	origin := r.Header.Get("Origin")
	if origin == "" && !contains(s.cfg.AllowedOrigins, "*") {
		return
	}
	if contains(s.cfg.AllowedOrigins, "*") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else if contains(s.cfg.AllowedOrigins, origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Idempotency-Key,X-Request-ID,X-Upload-Key")
	w.Header().Set("Access-Control-Expose-Headers", "Location,ETag,Last-Modified,Content-Range,Accept-Ranges,X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func (s *Server) applyCacheHeaders(w http.ResponseWriter) {
	switch s.cfg.HTTPCache.Mode {
	case "no-store":
		w.Header().Set("Cache-Control", "no-store")
	case "public":
		sMaxAge := s.cfg.HTTPCache.SMaxAge
		if sMaxAge <= 0 {
			sMaxAge = s.cfg.HTTPCache.MaxAge
		}
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, s-maxage=%d", int(s.cfg.HTTPCache.MaxAge.Seconds()), int(sMaxAge.Seconds())))
	default:
		w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", int(s.cfg.HTTPCache.MaxAge.Seconds())))
	}
}

func originAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return true
	}
	return contains(allowed, "*") || contains(allowed, origin)
}

func routeTemplate(pathValue string, s *Server) string {
	switch {
	case pathValue == "/healthz":
		return "/healthz"
	case pathValue == s.uploadBase+"/keys":
		return s.uploadBase + "/keys"
	case strings.HasPrefix(pathValue, s.uploadBase+"/keys/") && strings.HasSuffix(pathValue, "/content"):
		return s.uploadBase + "/keys/{upload_key}/content"
	case strings.HasPrefix(pathValue, s.uploadBase+"/keys/"):
		return s.uploadBase + "/keys/{upload_key}"
	case pathValue == s.uploadBase+"/wait":
		return s.uploadBase + "/wait"
	case pathValue == s.uploadBase+"/watch":
		return s.uploadBase + "/watch"
	case strings.HasPrefix(pathValue, s.fileBase+"/shared/"):
		return s.fileBase + "/shared/{shared_key}"
	case strings.HasPrefix(pathValue, s.fileBase+"/"):
		return s.fileBase + "/{key}"
	case strings.HasPrefix(pathValue, s.filesBase+"/"):
		return s.filesBase + "/{keys}"
	case strings.HasPrefix(pathValue, s.cfg.BackendBasePath+"/file/shared-keys"):
		return s.cfg.BackendBasePath + "/file/shared-keys"
	case strings.HasPrefix(pathValue, s.cfg.BackendBasePath):
		return s.cfg.BackendBasePath
	default:
		return pathValue
	}
}

type progressReader struct {
	reader io.Reader
	hash   hash.Hash
	n      int64
	notify func(int64)
}

type uploadInspection struct {
	prefix              []byte
	detectedContentType string
}

type securityUploadError struct {
	status  int
	code    string
	message string
}

func (e securityUploadError) Error() string {
	return e.message
}

func inspectUploadPrefix(reader io.Reader, declared, originalName string, policy config.MimeMagicPolicy) (uploadInspection, error) {
	if policy.PrefixBytes <= 0 {
		policy.PrefixBytes = 3072
	}
	limit := policy.PrefixBytes
	if limit < 512 {
		limit = 512
	}
	if limit > 1<<20 {
		limit = 1 << 20
	}
	prefix := make([]byte, limit)
	n, err := io.ReadFull(reader, prefix)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return uploadInspection{}, securityUploadError{
			status:  http.StatusBadRequest,
			code:    "prefix_read_failed",
			message: "uploaded content prefix could not be read",
		}
	}
	prefix = prefix[:n]
	detected := detectContentType(prefix)
	declared = normalizeContentType(declared)

	script := detectScript(prefix, originalName)
	allowedScript := script.detected && scriptAllowed(script, policy)
	if policy.RejectScriptUploads && script.detected && !allowedScript {
		return uploadInspection{}, securityUploadError{
			status:  http.StatusUnsupportedMediaType,
			code:    "script_upload_rejected",
			message: scriptRejectionMessage(script),
		}
	}
	if declared != "" && detected != "" && detected != "application/octet-stream" && !allowedScript && !mimeEquivalent(declared, detected, policy.EquivalentMIMETypes) && !declaredCompatibleWithDetected(declared, detected, originalName) {
		return uploadInspection{}, securityUploadError{
			status:  http.StatusUnsupportedMediaType,
			code:    "content_type_mismatch",
			message: fmt.Sprintf("declared content type %s does not match detected content type %s", declared, detected),
		}
	}
	if err := validateExtensionContentType(originalName, declared, detected, policy); err != nil {
		return uploadInspection{}, err
	}
	denyMIMETypes := policy.ExpandedDenyMIMETypes
	if denyMIMETypes == nil {
		denyMIMETypes = enabledMIMETypes(policy.DenyMIMETypes)
	}
	if mimeMatchesAny(detected, denyMIMETypes, policy.EquivalentMIMETypes) || mimeMatchesAny(declared, denyMIMETypes, policy.EquivalentMIMETypes) {
		return uploadInspection{}, securityUploadError{
			status:  http.StatusUnsupportedMediaType,
			code:    "content_type_denied",
			message: "uploaded content type is denied",
		}
	}
	allowMIMETypes := policy.ExpandedAllowMIMETypes
	if allowMIMETypes == nil {
		allowMIMETypes = enabledMIMETypes(policy.AllowMIMETypes)
	}
	if len(allowMIMETypes) > 0 && !mimeMatchesAny(detected, allowMIMETypes, policy.EquivalentMIMETypes) && !mimeMatchesAny(declared, allowMIMETypes, policy.EquivalentMIMETypes) {
		return uploadInspection{}, securityUploadError{
			status:  http.StatusUnsupportedMediaType,
			code:    "content_type_not_allowed",
			message: "uploaded content type is not allowed",
		}
	}
	return uploadInspection{prefix: prefix, detectedContentType: detected}, nil
}

func validateExtensionContentType(originalName, declared, detected string, policy config.MimeMagicPolicy) error {
	ext := normalizedExtension(originalName)
	if ext == "" {
		return nil
	}
	expected := config.MIMEFileType(ext)
	if len(expected) == 0 {
		return nil
	}
	if extensionMatchesMIME(detected, expected, policy.EquivalentMIMETypes) {
		return nil
	}
	if extensionMatchesMIME(declared, expected, policy.EquivalentMIMETypes) {
		if detected == "" || detected == "application/octet-stream" || mimeEquivalent(declared, detected, policy.EquivalentMIMETypes) || declaredCompatibleWithDetected(declared, detected, originalName) {
			return nil
		}
	}
	return securityUploadError{
		status:  http.StatusUnsupportedMediaType,
		code:    "file_extension_mismatch",
		message: fmt.Sprintf("file extension .%s does not match detected or declared content type", ext),
	}
}

func extensionMatchesMIME(value string, expected []string, equivalents [][]string) bool {
	value = normalizeContentType(value)
	if value == "" {
		return false
	}
	for _, candidate := range expected {
		if mimeEquivalent(value, candidate, equivalents) {
			return true
		}
	}
	return false
}

func detectContentType(prefix []byte) string {
	if len(prefix) == 0 {
		return ""
	}
	detected := normalizeContentType(mimetype.Detect(prefix).String())
	if detected == "" || detected == "application/octet-stream" {
		fallback := normalizeContentType(http.DetectContentType(prefix))
		if fallback != "" {
			detected = fallback
		}
	}
	return detected
}

func normalizeContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, _, err := stdmime.ParseMediaType(value); err == nil {
		return strings.ToLower(strings.TrimSpace(parsed))
	}
	if i := strings.IndexByte(value, ';'); i >= 0 {
		value = value[:i]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

type scriptDetection struct {
	detected     bool
	scriptType   string
	extension    string
	expectedMIME string
}

func detectScript(prefix []byte, originalName string) scriptDetection {
	out := scriptDetection{extension: normalizedScriptExtension(originalName)}
	if mimeType := scriptMIMEForExtension(out.extension); mimeType != "" {
		out.detected = true
		out.scriptType = scriptTypeForExtension(out.extension)
		out.expectedMIME = mimeType
	}
	if !bytes.HasPrefix(prefix, []byte("#!")) {
		return out
	}
	line := prefix
	if i := bytes.IndexByte(prefix, '\n'); i >= 0 {
		line = prefix[:i]
	}
	lower := strings.ToLower(string(line))
	for _, candidate := range []string{"bash", "zsh", "dash", "ksh", "sh", "python3", "python", "nodejs", "node", "ruby", "perl", "php"} {
		if strings.Contains(lower, "/"+candidate) || strings.Contains(lower, "env "+candidate) {
			out.detected = true
			out.scriptType = normalizeScriptType(candidate)
			out.expectedMIME = scriptMIMEForType(out.scriptType)
			return out
		}
	}
	out.detected = true
	if out.scriptType == "" {
		out.scriptType = "shell"
		out.expectedMIME = scriptMIMEForType(out.scriptType)
	}
	return out
}

func scriptAllowed(script scriptDetection, policy config.MimeMagicPolicy) bool {
	if script.scriptType != "" && policy.AllowedScriptTypes[script.scriptType] {
		return true
	}
	if script.extension != "" && policy.AllowedScriptExtensions[script.extension] {
		return true
	}
	return false
}

func scriptRejectionMessage(script scriptDetection) string {
	scriptType := script.scriptType
	if scriptType == "" {
		scriptType = "unknown"
	}
	expected := script.expectedMIME
	if expected == "" {
		expected = "a script-specific MIME type"
	}
	return fmt.Sprintf("detected %s script; expected MIME %s; enable mime_magic.allowed_script_types.%s or an allowed script extension to accept it", scriptType, expected, scriptType)
}

func normalizeScriptType(value string) string {
	switch value {
	case "bash", "zsh", "dash", "ksh", "sh":
		return "shell"
	case "python3":
		return "python"
	case "nodejs":
		return "node"
	default:
		return value
	}
}

func scriptTypeForExtension(ext string) string {
	switch ext {
	case "sh", "bash", "zsh", "ksh":
		return "shell"
	case "py":
		return "python"
	case "js", "mjs", "cjs":
		return "node"
	case "rb":
		return "ruby"
	case "pl":
		return "perl"
	case "php":
		return "php"
	case "ps1", "psm1", "psd1":
		return "powershell"
	case "bat", "cmd":
		return "batch"
	case "makefile", "gnumakefile":
		return "make"
	default:
		return ""
	}
}

func scriptMIMEForExtension(ext string) string {
	return scriptMIMEForType(scriptTypeForExtension(ext))
}

func scriptMIMEForType(scriptType string) string {
	switch scriptType {
	case "shell":
		return "text/x-shellscript"
	case "python":
		return "text/x-python"
	case "node":
		return "text/javascript"
	case "ruby":
		return "text/x-ruby"
	case "perl":
		return "text/x-perl"
	case "php":
		return "application/x-httpd-php"
	case "powershell":
		return "text/x-powershell"
	case "batch":
		return "application/x-bat"
	case "make":
		return "text/x-makefile"
	default:
		return ""
	}
}

func normalizedScriptExtension(name string) string {
	base := strings.ToLower(path.Base(name))
	switch base {
	case "makefile", "gnumakefile":
		return base
	default:
		return normalizedExtension(name)
	}
}

func mimeMatchesAny(value string, candidates []string, equivalents [][]string) bool {
	if value == "" {
		return false
	}
	for _, candidate := range candidates {
		if mimeEquivalent(value, candidate, equivalents) {
			return true
		}
	}
	return false
}

func enabledMIMETypes(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value, enabled := range values {
		if enabled {
			out = append(out, value)
		}
	}
	return out
}

func mimeEquivalent(a, b string, equivalents [][]string) bool {
	a = normalizeContentType(a)
	b = normalizeContentType(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	for _, group := range equivalents {
		hasA := false
		hasB := false
		for _, value := range group {
			normalized := normalizeContentType(value)
			hasA = hasA || normalized == a
			hasB = hasB || normalized == b
		}
		if hasA && hasB {
			return true
		}
	}
	return false
}

func declaredCompatibleWithDetected(declared, detected, originalName string) bool {
	declared = normalizeContentType(declared)
	detected = normalizeContentType(detected)
	if declared == "" || detected == "" {
		return false
	}
	compatible := map[string][]string{
		"text/plain": {
			"application/json",
			"application/x-python",
			"application/x-ndjson",
			"application/yaml",
			"application/x-yaml",
			"text/yaml",
			"text/x-yaml",
			"text/x-python",
			"text/x-script.python",
		},
		"text/markdown": {
			"text/plain",
			"text/html",
		},
		"application/octet-stream": {
			"text/plain",
			"application/vnd.microsoft.portable-executable",
			"application/x-coredump",
			"application/x-dosexec",
			"application/x-elf",
			"application/x-executable",
			"application/x-mach-binary",
			"application/x-msdownload",
			"application/x-object",
			"application/x-sharedlib",
		},
	}
	if declared == "text/plain" && strings.HasSuffix(detected, "+json") {
		return true
	}
	if detected == "text/plain" && declaredTextDerivedMIME(declared) {
		return true
	}
	if extensionCompatibleWithDetected(declared, detected, originalName) {
		return true
	}
	for _, candidate := range compatible[declared] {
		if detected == candidate {
			return true
		}
	}
	return false
}

func declaredTextDerivedMIME(declared string) bool {
	if strings.HasSuffix(declared, "+json") || strings.HasSuffix(declared, "+xml") {
		return true
	}
	switch declared {
	case "text/plain",
		"text/csv",
		"text/markdown",
		"text/html",
		"text/xml",
		"text/yaml",
		"text/x-yaml",
		"application/json",
		"application/x-ndjson",
		"application/yaml",
		"application/x-yaml",
		"application/xml",
		"application/xhtml+xml",
		"text/x-python",
		"application/x-python",
		"text/x-script.python",
		"text/javascript",
		"application/javascript",
		"text/x-shellscript",
		"text/x-ruby",
		"text/x-perl",
		"application/x-httpd-php",
		"text/x-powershell",
		"text/x-makefile",
		"application/x-bat":
		return true
	default:
		return false
	}
}

func extensionCompatibleWithDetected(declared, detected, originalName string) bool {
	ext := normalizedExtension(originalName)
	switch ext {
	case "docx", "xlsx", "pptx", "odt", "ods", "odp":
		return detected == "application/zip"
	case "md", "markdown":
		return declared == "text/markdown" && (detected == "text/plain" || detected == "text/html")
	case "rst", "rest":
		return (declared == "application/octet-stream" || declared == "text/plain") && detected == "text/plain"
	case "txt", "text":
		return declared == "application/octet-stream" && detected == "text/plain"
	default:
		return false
	}
}

func normalizedExtension(name string) string {
	return strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
}

func securityErrorResponse(err error) (int, string) {
	var uploadErr securityUploadError
	if errors.As(err, &uploadErr) {
		return uploadErr.status, uploadErr.code
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return http.StatusRequestEntityTooLarge, "upload_too_large"
	}
	return http.StatusUnsupportedMediaType, "content_rejected"
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.n += int64(n)
		_, _ = r.hash.Write(p[:n])
		if r.notify != nil {
			r.notify(r.n)
		}
	}
	return n, err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func (s *Server) uploadOwnerToken(w http.ResponseWriter, r *http.Request) string {
	const cookieName = "streamuploader_owner"
	if cookie, err := r.Cookie(cookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return cookie.Value
	}
	token := randomToken(32)
	maxAge := int(s.cfg.SessionTTL.Seconds())
	if maxAge <= 0 {
		maxAge = int((24 * time.Hour).Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     s.uploadBase,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(strings.ToLower(s.cfg.PublicBaseURL), "https://"),
	})
	return token
}

func (s *Server) uploadOwnerMatches(r *http.Request, wantHash string) bool {
	if wantHash == "" {
		return false
	}
	cookie, err := r.Cookie("streamuploader_owner")
	if err != nil {
		return false
	}
	return hashToken(cookie.Value) == wantHash
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "=")
}

func safeSegment(value string) string {
	value = path.Base(strings.TrimSpace(value))
	value = safeSegmentPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, ".-_")
	if value == "" {
		return "file"
	}
	if len(value) > 180 {
		return value[:180]
	}
	return value
}

func contentDispositionAttachment(fileName string) string {
	return fmt.Sprintf(`attachment; filename="%s"`, strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(fileName))
}

func lowerMetadata(metadata map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range metadata {
		out[strings.ToLower(key)] = value
	}
	return out
}

func archiveEntryName(objectKey string, metadata map[string]string) string {
	metadata = lowerMetadata(metadata)
	if value := strings.TrimSpace(metadata["original-name"]); value != "" {
		return safeSegment(value)
	}
	return safeSegment(path.Base(objectKey))
}

func uniqueArchiveName(name string, used map[string]int) string {
	if name == "" {
		name = "file"
	}
	count := used[name]
	used[name] = count + 1
	if count == 0 {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s-%d%s", base, count+1, ext)
}

func storagePrefix(uploadKey, requested string) string {
	if requested != "" {
		return path.Join("uploads", safeSegment(requested), uploadKey)
	}
	return path.Join("uploads", uploadKey)
}

func temporaryUploadObjectKey(objectKey, uploadKey string) string {
	return path.Join(path.Dir(objectKey), ".tmp", uploadKey+"-"+path.Base(objectKey))
}

func displayedUploadBytes(actual, size int64, holdForPostUploadCheck bool) int64 {
	if !holdForPostUploadCheck || size <= 0 {
		return actual
	}
	capBytes := size * 98 / 100
	if capBytes < 0 {
		capBytes = 0
	}
	if actual > capBytes {
		return capBytes
	}
	return actual
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func terminal(status model.UploadStatus) bool {
	return status == model.UploadUploaded || status == model.UploadFailed || status == model.UploadExpired || status == model.UploadCanceled
}

func normalizedTaskKinds(kinds []string) []string {
	if len(kinds) == 0 {
		return []string{"image_thumbnail"}
	}
	out := make([]string, 0, len(kinds))
	seen := map[string]struct{}{}
	for _, kind := range kinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	if len(out) == 0 {
		return []string{"image_thumbnail"}
	}
	return out
}

func splitQueryValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func queryBool(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && parsed
}

func includeSet(values []string) map[string]bool {
	parts := splitQueryValues(values)
	if len(parts) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, part := range parts {
		switch part {
		case "text", "extracted", "title", "description", "ocr", "metadata", "sources":
			out[part] = true
		}
	}
	return out
}

func positiveQueryInt(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func cloneUpload(item *model.UploadItem) *model.UploadItem {
	if item == nil {
		return nil
	}
	copyItem := *item
	if item.UploadedAt != nil {
		uploadedAt := *item.UploadedAt
		copyItem.UploadedAt = &uploadedAt
	}
	if item.Thumbnail != nil {
		thumbnailCopy := *item.Thumbnail
		copyItem.Thumbnail = &thumbnailCopy
	}
	if item.ExtractedContent != nil {
		extractedCopy := *item.ExtractedContent
		copyItem.ExtractedContent = &extractedCopy
	}
	return &copyItem
}

func mustUpload(item *model.UploadItem, _ bool) *model.UploadItem {
	return item
}

func Run(ctx context.Context, cfg config.Config, store storage.Store) error {
	app := New(cfg, store)
	if cfg.UploadDeadlines.Enabled && cfg.UploadDeadlines.CleanupEnabled && cfg.UploadDeadlines.CleanupMode == "server_loop" {
		go app.runCleanupLoop(ctx)
	}
	httpServer := &http.Server{Addr: cfg.Addr, Handler: app.Handler()}
	servers := []*http.Server{httpServer}
	errCh := make(chan error, 2)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	if cfg.BackendAddr != "" {
		backendServer := &http.Server{Addr: cfg.BackendAddr, Handler: app.BackendHandler()}
		servers = append(servers, backendServer)
		go func() {
			errCh <- backendServer.ListenAndServe()
		}()
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, srv := range servers {
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return err
			}
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func CleanupOnce(ctx context.Context, cfg config.Config, store storage.Store) error {
	return New(cfg, store).CleanupExpiredUploads(ctx)
}

func (s *Server) runCleanupLoop(ctx context.Context) {
	if s.cfg.UploadDeadlines.CleanupInterval <= 0 {
		return
	}
	_ = s.CleanupExpiredUploads(ctx)
	ticker := time.NewTicker(s.cfg.UploadDeadlines.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.CleanupExpiredUploads(ctx); err != nil {
				slog.Warn("cleanup_failed", "error", err)
			}
		}
	}
}
