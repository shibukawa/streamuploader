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
	"math"
	stdmime "mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gorilla/websocket"

	"streamuploader/internal/config"
	"streamuploader/internal/model"
	"streamuploader/internal/storage"
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
	if cfg.Security.MimeMagic.PrefixBytes <= 0 {
		cfg.Security = config.DefaultSecurityPolicy()
	}
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
	return &Server{
		cfg:        cfg,
		store:      store,
		proxy:      proxy,
		upgrader:   websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		uploads:    map[string]*model.UploadItem{},
		watchers:   map[string]map[chan model.WatchServerMessage]struct{}{},
		uploadBase: strings.TrimRight(cfg.UploadBasePath, "/"),
		fileBase:   "/api/file",
		filesBase:  "/api/files",
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

func (s *Server) BackendHandler() http.Handler {
	return http.HandlerFunc(s.backendRoute)
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
		expiresAt = parsed.UTC().Format(time.RFC3339)
	} else if req.TTLSeconds > 0 {
		expiresAt = now.Add(time.Duration(req.TTLSeconds) * time.Second).Format(time.RFC3339)
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
	now := time.Now().UTC()
	uploadKey := randomToken(24)
	fileName := safeSegment(req.FileName)
	prefix := storagePrefix(uploadKey, req.Prefix)
	objectKey := path.Join(prefix, fileName)
	item := &model.UploadItem{
		UploadKey:     uploadKey,
		Role:          req.Role,
		OriginalName:  req.FileName,
		ContentType:   req.ContentType,
		SizeBytes:     req.SizeBytes,
		StoragePrefix: prefix,
		ObjectKey:     objectKey,
		DisplayKey:    objectKey,
		Status:        model.UploadKeyCreated,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(s.cfg.SessionTTL),
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
		MaxUploadBytes: s.cfg.MaxUploadBytes,
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
	contentType := target.contentType
	if contentType == "" {
		contentType = r.Header.Get("Content-Type")
	}
	s.updateUpload(uploadKey, func(item *model.UploadItem) {
		item.Status = model.UploadUploading
		item.ContentType = contentType
		item.UpdatedAt = time.Now().UTC()
	})

	limited := http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
	body := io.Reader(limited)
	var inspectedPrefix []byte
	archiveKind := archiveKindFor(contentType, target.originalName)
	if s.cfg.Security.MimeMagic.Enabled {
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
	}
	archiveUpload := s.cfg.Security.ArchiveGuard.Enabled && archiveKind != archiveUnknown
	objectKey := target.objectKey
	if archiveUpload {
		objectKey = temporaryUploadObjectKey(target.objectKey, uploadKey)
	}
	measured := &progressReader{
		reader: body,
		hash:   sha256.New(),
		notify: func(n int64) {
			s.updateUpload(uploadKey, func(item *model.UploadItem) {
				item.UploadedBytes = n
				item.UpdatedAt = time.Now().UTC()
			})
			s.broadcast(model.WatchServerMessage{
				Type:          "progress",
				UploadKey:     uploadKey,
				UploadedBytes: n,
				SizeBytes:     target.sizeBytes,
				Status:        model.UploadUploading,
			})
		},
	}
	result, err := s.store.PutObject(r.Context(), storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         objectKey,
		Body:        measured,
		ContentType: contentType,
	})
	if err != nil {
		code := "storage_error"
		status := http.StatusBadGateway
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			code = "upload_too_large"
			status = http.StatusRequestEntityTooLarge
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
		if err := inspectArchiveObject(r.Context(), s.store, s.cfg.Bucket, objectKey, measured.n, archiveKind, s.cfg.Security.ArchiveGuard); err != nil {
			_ = s.store.DeleteObject(r.Context(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
			s.failUpload(uploadKey, err.Error())
			status, code := securityErrorResponse(err)
			writeError(w, status, code, err.Error())
			return
		}
		copyResult, err := s.store.CopyObject(r.Context(), storage.CopyInput{
			Bucket:      s.cfg.Bucket,
			SourceKey:   objectKey,
			Key:         target.objectKey,
			ContentType: contentType,
		})
		_ = s.store.DeleteObject(r.Context(), storage.DeleteInput{Bucket: s.cfg.Bucket, Key: objectKey})
		if err != nil {
			s.failUpload(uploadKey, err.Error())
			writeError(w, http.StatusBadGateway, "storage_error", err.Error())
			return
		}
		if copyResult.ETag != "" {
			result.ETag = copyResult.ETag
		}
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
		uploaded = cloneUpload(item)
	})
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
	if attachment {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(fileName))
	}
	if out.ETag != "" {
		w.Header().Set("ETag", out.ETag)
	}
	if out.ContentRange != "" {
		w.Header().Set("Content-Range", out.ContentRange)
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
		items = append(items, item)
	}
	return items, ready
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
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Idempotency-Key,X-Request-ID,X-Upload-Key")
	w.Header().Set("Access-Control-Expose-Headers", "Location,ETag,X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
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
	if declared != "" && detected != "" && !allowedScript && !mimeEquivalent(declared, detected, policy.EquivalentMIMETypes) {
		return uploadInspection{}, securityUploadError{
			status:  http.StatusUnsupportedMediaType,
			code:    "content_type_mismatch",
			message: fmt.Sprintf("declared content type %s does not match detected content type %s", declared, detected),
		}
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
	out := scriptDetection{extension: strings.TrimPrefix(strings.ToLower(path.Ext(originalName)), ".")}
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
	default:
		return ""
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
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
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

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func terminal(status model.UploadStatus) bool {
	return status == model.UploadUploaded || status == model.UploadFailed || status == model.UploadExpired
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
	return &copyItem
}

func mustUpload(item *model.UploadItem, _ bool) *model.UploadItem {
	return item
}

func Run(ctx context.Context, cfg config.Config, store storage.Store) error {
	app := New(cfg, store)
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
