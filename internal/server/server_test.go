package server

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/gorilla/websocket"
	"github.com/klauspost/compress/zstd"

	"streamuploader/internal/config"
	"streamuploader/internal/storage"
)

type fakeStore struct {
	mu       sync.Mutex
	objects  map[string][]byte
	metadata map[string]map[string]string
	modified map[string]time.Time
	copies   int
}

func (s *fakeStore) PutObject(_ context.Context, input storage.PutInput) (storage.PutResult, error) {
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return storage.PutResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[input.Key] = body
	if s.metadata == nil {
		s.metadata = map[string]map[string]string{}
	}
	if s.modified == nil {
		s.modified = map[string]time.Time{}
	}
	s.metadata[input.Key] = input.Metadata
	s.modified[input.Key] = time.Now().UTC()
	return storage.PutResult{ETag: `"fake"`}, nil
}

func (s *fakeStore) GetObject(_ context.Context, input storage.GetInput) (storage.GetResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[input.Key]
	if !ok {
		return storage.GetResult{}, errFakeNotFound
	}
	contentRange := ""
	if input.Range != "" {
		start, end, ok := parseTestRange(input.Range, int64(len(body)))
		if !ok {
			return storage.GetResult{}, errFakeNotFound
		}
		contentRange = fmt.Sprintf("bytes %d-%d/%d", start, end, len(body))
		body = body[start : end+1]
	}
	return storage.GetResult{
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentType:   "application/octet-stream",
		ContentLength: int64(len(body)),
		ETag:          `"fake"`,
		LastModified:  s.modified[input.Key],
		Metadata:      s.metadata[input.Key],
		ContentRange:  contentRange,
	}, nil
}

func (s *fakeStore) CopyObject(_ context.Context, input storage.CopyInput) (storage.CopyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[input.SourceKey]
	if !ok {
		return storage.CopyResult{}, errFakeNotFound
	}
	s.copies++
	s.objects[input.Key] = append([]byte(nil), body...)
	if s.metadata == nil {
		s.metadata = map[string]map[string]string{}
	}
	if s.modified == nil {
		s.modified = map[string]time.Time{}
	}
	s.metadata[input.Key] = input.Metadata
	s.modified[input.Key] = time.Now().UTC()
	return storage.CopyResult{ETag: `"fake-copy"`}, nil
}

func (s *fakeStore) HeadObject(_ context.Context, input storage.HeadInput) (storage.HeadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[input.Key]
	if !ok {
		return storage.HeadResult{}, errFakeNotFound
	}
	return storage.HeadResult{
		ContentType:   "application/octet-stream",
		ContentLength: int64(len(body)),
		ETag:          `"fake"`,
		LastModified:  s.modified[input.Key],
		Metadata:      s.metadata[input.Key],
	}, nil
}

func (s *fakeStore) ListObjects(_ context.Context, input storage.ListInput) (storage.ListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for key := range s.objects {
		if strings.HasPrefix(key, input.Prefix) {
			keys = append(keys, key)
		}
	}
	return storage.ListResult{Keys: keys}, nil
}

func (s *fakeStore) PresignGetObject(_ context.Context, input storage.PresignGetInput) (storage.PresignGetResult, error) {
	return storage.PresignGetResult{URL: "http://s3.test/" + url.PathEscape(input.Key), ExpiresAt: time.Now().Add(input.Expires)}, nil
}

func (s *fakeStore) DeleteObject(_ context.Context, input storage.DeleteInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, input.Key)
	delete(s.metadata, input.Key)
	delete(s.modified, input.Key)
	return nil
}

var errFakeNotFound = errors.New("not found")

func parseTestRange(value string, size int64) (int64, int64, bool) {
	if !strings.HasPrefix(value, "bytes=") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if start < 0 || end < start || start >= size {
		return 0, 0, false
	}
	if end >= size {
		end = size - 1
	}
	return start, end, true
}

func TestUploadKeyFlow(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"hello.txt","content_type":"text/plain"}`)
	if keyResp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d", keyResp.StatusCode)
	}
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)
	if key.UploadKey == "" || key.ObjectKey == "" {
		t.Fatalf("missing key response: %+v", key)
	}

	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewBufferString("hello"))
	uploadReq.Header.Set("Content-Type", "text/plain")
	uploadResp := do(t, uploadReq)
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", uploadResp.StatusCode)
	}
	if got := string(store.objects[key.ObjectKey]); got != "hello" {
		t.Fatalf("stored object = %q", got)
	}
	if store.copies != 0 {
		t.Fatalf("non-archive upload used copy promotion: copies=%d", store.copies)
	}
	assertNoTmpObjects(t, store)

	waitResp := postJSON(t, app.URL+"/api/upload/wait", `{"upload_keys":["`+key.UploadKey+`"],"timeout_seconds":1}`)
	if waitResp.StatusCode != http.StatusOK {
		t.Fatalf("wait status = %d", waitResp.StatusCode)
	}
	var wait struct {
		Ready bool `json:"ready"`
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	decode(t, waitResp, &wait)
	if !wait.Ready || len(wait.Items) != 1 || wait.Items[0].Status != "uploaded" {
		t.Fatalf("wait response = %+v", wait)
	}
}

func TestUploadRejectsMimeMagicMismatchBeforeStorage(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		Security:       config.DefaultSecurityPolicy(),
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"fake.jpg","content_type":"image/jpeg"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0}, 128)...)
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(elf))
	uploadReq.Header.Set("Content-Type", "image/jpeg")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusUnsupportedMediaType {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	var body map[string]string
	if err := json.NewDecoder(uploadResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "content_type_mismatch" {
		t.Fatalf("error body = %+v", body)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected object was stored")
	}
	statusResp, err := http.Get(app.URL + "/api/upload/keys/" + key.UploadKey)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	decode(t, statusResp, &status)
	if status.Status != "failed" || !strings.Contains(status.Error, "declared content type") {
		t.Fatalf("upload status = %+v", status)
	}
}

func TestUploadRejectsScriptDeclaredAsText(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		Security:       config.DefaultSecurityPolicy(),
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"note.txt","content_type":"text/plain"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", strings.NewReader("#!/bin/bash\necho denied\n"))
	uploadReq.Header.Set("Content-Type", "text/plain")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusUnsupportedMediaType {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	var body map[string]string
	if err := json.NewDecoder(uploadResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "script_upload_rejected" {
		t.Fatalf("error body = %+v", body)
	}
	if !strings.Contains(body["message"], "text/x-shellscript") {
		t.Fatalf("error message = %+v", body)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected script was stored")
	}
}

func TestUploadAllowsConfiguredScriptType(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.AllowedScriptTypes = map[string]bool{"shell": true}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		Security:       security,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"run.sh","content_type":"text/plain"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	body := []byte("#!/bin/bash\necho allowed\n")
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(body))
	uploadReq.Header.Set("Content-Type", "text/plain")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(respBody))
	}
	store.mu.Lock()
	got := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if !bytes.Equal(got, body) {
		t.Fatalf("stored object = %q", string(got))
	}
}

func TestUploadMimeMagicCheckCanOptOut(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.Enabled = false
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		Security:       security,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"fake.jpg","content_type":"image/jpeg"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0}, 128)...)
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(elf))
	uploadReq.Header.Set("Content-Type", "image/jpeg")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	store.mu.Lock()
	got := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if !bytes.Equal(got, elf) {
		t.Fatalf("stored object = %x", got)
	}
}

func TestUploadAllowFileTypeCategory(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.AllowFileTypes = map[string]bool{"images": true}
	security = normalizeTestSecurityPolicy(security)
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		Security:       security,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"pixel.png","content_type":"image/png"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(png))
	uploadReq.Header.Set("Content-Type", "image/png")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	store.mu.Lock()
	got := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if !bytes.Equal(got, png) {
		t.Fatalf("stored object = %x", got)
	}
}

func TestUploadAllowFileTypeCategoryRejectsOutsideType(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.AllowFileTypes = map[string]bool{"images": true}
	security = normalizeTestSecurityPolicy(security)
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		Security:       security,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"doc.pdf","content_type":"application/pdf"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(pdf))
	uploadReq.Header.Set("Content-Type", "application/pdf")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusUnsupportedMediaType {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	var body map[string]string
	if err := json.NewDecoder(uploadResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "content_type_not_allowed" {
		t.Fatalf("error body = %+v", body)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected object was stored")
	}
}

func TestUploadRejectsZipArchiveBombBeforeStorage(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.Enabled = false
	security.ArchiveGuard.MaxTotalUncompressedBytes = 512
	security.ArchiveGuard.MaxSingleEntryBytes = 512
	security.ArchiveGuard.MaxCompressionRatio = 1000
	cfg := testUploadConfig(security)
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	body := makeZip(t, "huge.txt", bytes.Repeat([]byte("a"), 1024))
	resp, key := uploadTestObject(t, app.URL, "huge.zip", "application/zip", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var errorBody map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody["error"] != "archive_too_large" {
		t.Fatalf("error body = %+v", errorBody)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected zip was stored")
	}
	assertNoTmpObjects(t, store)
}

func TestUploadRejectsGzipArchiveBombBeforeStorage(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.Enabled = false
	security.ArchiveGuard.MaxTotalUncompressedBytes = 512
	security.ArchiveGuard.MaxSingleEntryBytes = 512
	security.ArchiveGuard.MaxCompressionRatio = 1000
	cfg := testUploadConfig(security)
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	body := makeGzip(t, bytes.Repeat([]byte("a"), 1024))
	resp, key := uploadTestObject(t, app.URL, "huge.gz", "application/gzip", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var errorBody map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody["error"] != "archive_too_large" {
		t.Fatalf("error body = %+v", errorBody)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected gzip was stored")
	}
	assertNoTmpObjects(t, store)
}

func TestUploadAllowsBoundedZstdAndBrotliArchives(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.MimeMagic.Enabled = false
	security.ArchiveGuard.MaxTotalUncompressedBytes = 2048
	security.ArchiveGuard.MaxSingleEntryBytes = 2048
	security.ArchiveGuard.MaxCompressionRatio = 1000
	cfg := testUploadConfig(security)
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	zstdBody := makeZstd(t, []byte("small zstd payload"))
	zstdResp, zstdKey := uploadTestObject(t, app.URL, "small.zst", "application/zstd", zstdBody)
	defer zstdResp.Body.Close()
	if zstdResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(zstdResp.Body)
		t.Fatalf("zstd upload status = %d body=%q", zstdResp.StatusCode, string(respBody))
	}
	brotliBody := makeBrotli(t, []byte("small brotli payload"))
	brotliResp, brotliKey := uploadTestObject(t, app.URL, "small.br", "application/x-brotli", brotliBody)
	defer brotliResp.Body.Close()
	if brotliResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(brotliResp.Body)
		t.Fatalf("brotli upload status = %d body=%q", brotliResp.StatusCode, string(respBody))
	}
	store.mu.Lock()
	gotZstd := store.objects[zstdKey.ObjectKey]
	gotBrotli := store.objects[brotliKey.ObjectKey]
	store.mu.Unlock()
	if !bytes.Equal(gotZstd, zstdBody) {
		t.Fatal("stored zstd body changed")
	}
	if !bytes.Equal(gotBrotli, brotliBody) {
		t.Fatal("stored brotli body changed")
	}
	assertNoTmpObjects(t, store)
}

func TestUploadWithClamAVScansTmpThenPublishes(t *testing.T) {
	clamAddr, scannedBody, closeClam := startFakeClamAV(t, "stream: OK\x00")
	defer closeClam()

	security := config.DefaultSecurityPolicy()
	security.ClamAV.Enabled = true
	security.ClamAV.Address = clamAddr
	cfg := testUploadConfig(security)
	store := &fakeStore{objects: map[string][]byte{}}
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, key := uploadTestObject(t, srv.URL, "note.txt", "text/plain", []byte("hello from clamav"))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(body))
	}
	_ = resp.Body.Close()
	if got := string(<-scannedBody); got != "hello from clamav" {
		t.Fatalf("scanned body = %q", got)
	}
	if string(store.objects[key.ObjectKey]) != "hello from clamav" {
		t.Fatalf("published body = %q", string(store.objects[key.ObjectKey]))
	}
	if store.copies != 1 {
		t.Fatalf("copies = %d", store.copies)
	}
	assertNoTmpObjects(t, store)
}

func TestUploadWithClamAVRejectsDetectionAndDeletesTmp(t *testing.T) {
	clamAddr, scannedBody, closeClam := startFakeClamAV(t, "stream: Eicar-Test-Signature FOUND\x00")
	defer closeClam()

	security := config.DefaultSecurityPolicy()
	security.ClamAV.Enabled = true
	security.ClamAV.Address = clamAddr
	cfg := testUploadConfig(security)
	store := &fakeStore{objects: map[string][]byte{}}
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, key := uploadTestObject(t, srv.URL, "note.txt", "text/plain", []byte("bad content"))
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(body))
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["error"] != "malware_detected" {
		t.Fatalf("error = %q", body["error"])
	}
	if got := string(<-scannedBody); got != "bad content" {
		t.Fatalf("scanned body = %q", got)
	}
	if _, ok := store.objects[key.ObjectKey]; ok {
		t.Fatalf("infected object was published at %s", key.ObjectKey)
	}
	assertNoTmpObjects(t, store)
}

func TestReverseProxyNonUploadPaths(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("proxied"))
	}))
	defer upstream.Close()

	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:                 "simple_fronting_reverse_proxy",
		PublicBaseURL:        "http://example.test",
		UploadBasePath:       "/api/upload",
		ApplicationServerURL: upstream.URL,
		Bucket:               "bucket",
		SessionTTL:           time.Hour,
		MaxUploadBytes:       1024,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp, err := http.Get(app.URL + "/demo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "proxied" {
		t.Fatalf("proxy body = %q", body)
	}
}

func TestWatchUploadSnapshot(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()
	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"hello.txt"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
	}
	decode(t, keyResp, &key)

	wsURL := "ws" + strings.TrimPrefix(app.URL, "http") + "/api/upload/watch"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "watch", "upload_keys": []string{key.UploadKey}}); err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Type      string `json:"type"`
		UploadKey string `json:"upload_key"`
		Item      struct {
			Status string `json:"status"`
		} `json:"item"`
	}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "snapshot" || msg.UploadKey != key.UploadKey || msg.Item.Status != "key_created" {
		t.Fatalf("watch message = %+v", msg)
	}
}

func TestUploadKeyStartDeadlineExpires(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		UploadDeadlines: config.UploadDeadlinePolicy{
			Enabled:         true,
			MarkerPrefix:    ".uploading/",
			StartTimeout:    time.Nanosecond,
			FinishTimeout:   time.Minute,
			CleanupEnabled:  false,
			CleanupInterval: time.Minute,
			CleanupMode:     "disabled",
		},
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"late.txt","content_type":"text/plain"}`)
	var key struct {
		UploadKey string `json:"upload_key"`
	}
	decode(t, keyResp, &key)
	time.Sleep(time.Millisecond)

	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewBufferString("late"))
	uploadReq.Header.Set("Content-Type", "text/plain")
	uploadResp := do(t, uploadReq)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusGone {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	var body map[string]string
	if err := json.NewDecoder(uploadResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "upload_key_expired" {
		t.Fatalf("error body = %+v", body)
	}
}

func TestCleanupExpiredUploadMarkers(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		UploadDeadlines: config.UploadDeadlinePolicy{
			Enabled:         true,
			MarkerPrefix:    ".uploading/",
			StartTimeout:    time.Nanosecond,
			FinishTimeout:   time.Minute,
			CleanupEnabled:  false,
			CleanupInterval: time.Minute,
			CleanupMode:     "disabled",
		},
	}
	srv := New(cfg, store)
	app := httptest.NewServer(srv.Handler())
	defer app.Close()
	resp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"stale.txt"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d", resp.StatusCode)
	}
	time.Sleep(time.Millisecond)
	if err := srv.CleanupExpiredUploads(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for key := range store.objects {
		if strings.HasPrefix(key, ".uploading/") {
			t.Fatalf("stale marker remains: %s", key)
		}
	}
}

func TestSharedKeyUsesConfiguredDefaultTTL(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{"uploads/a/file.txt": []byte("hello")}}
	cfg := config.Config{
		PublicBaseURL:           "http://example.test",
		Bucket:                  "bucket",
		BackendBasePath:         "/internal",
		EnableSharedKey:         true,
		AllowFrontendFileAccess: true,
		SharedKeyBits:           128,
		SharedKeyPrefix:         ".streamuploader/shared/",
		SharedKeyTTL:            time.Hour,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp := postJSON(t, app.URL+"/internal/file/shared-keys", `{"object_key":"uploads/a/file.txt"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("shared key status = %d", resp.StatusCode)
	}
	var body struct {
		SharedKey string `json:"shared_key"`
		ExpiresAt string `json:"expires_at"`
	}
	decode(t, resp, &body)
	if body.SharedKey == "" || body.ExpiresAt == "" {
		t.Fatalf("shared key response = %+v", body)
	}
}

func TestDownloadHeadersUseCachePolicy(t *testing.T) {
	objectKey := "uploads/a/file.txt"
	store := &fakeStore{
		objects:  map[string][]byte{objectKey: []byte("hello")},
		metadata: map[string]map[string]string{},
		modified: map[string]time.Time{objectKey: time.Now().UTC()},
	}
	cfg := config.Config{
		Bucket:                  "bucket",
		AllowFrontendFileAccess: true,
		HTTPCache: config.HTTPCachePolicy{
			Mode:           "public",
			MaxAge:         2 * time.Hour,
			ForwardETag:    true,
			ForwardLastMod: true,
		},
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp, err := http.Get(app.URL + "/api/file/" + url.PathEscape(objectKey) + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff header = %q", resp.Header.Get("X-Content-Type-Options"))
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=7200, s-maxage=7200" {
		t.Fatalf("cache-control = %q", got)
	}
	if resp.Header.Get("ETag") == "" || resp.Header.Get("Last-Modified") == "" {
		t.Fatalf("missing validators: etag=%q last-modified=%q", resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"))
	}
}

func TestBackendDeleteObjectSamePort(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{"uploads/test/file.txt": []byte("hello")}}
	cfg := config.Config{
		Mode:             "standalone_cross_origin",
		PublicBaseURL:    "http://example.test",
		UploadBasePath:   "/api/upload",
		BackendBasePath:  "/internal",
		BackendAuthToken: "secret",
		AllowedOrigins:   []string{"*"},
		Bucket:           "bucket",
		SessionTTL:       time.Hour,
		MaxUploadBytes:   1024,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	req, _ := http.NewRequest(http.MethodDelete, app.URL+"/internal/objects/"+url.PathEscape("uploads/test/file.txt"), nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp := do(t, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	store.mu.Lock()
	_, ok := store.objects["uploads/test/file.txt"]
	store.mu.Unlock()
	if ok {
		t.Fatal("object was not deleted")
	}
}

func TestBackendHealthHandler(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		BackendBasePath: "/internal",
		Bucket:          "bucket",
		SessionTTL:      time.Hour,
		MaxUploadBytes:  1024,
	}
	app := httptest.NewServer(New(cfg, store).BackendHandler())
	defer app.Close()

	resp, err := http.Get(app.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

func TestFileProxyDownload(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{"uploads/test/hello.txt": []byte("hello")}}
	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1024,
		AllowFrontendFileAccess: true,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp, err := http.Get(app.URL + "/api/file/" + url.PathEscape("uploads/test/hello.txt") + "/content")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello" {
		t.Fatalf("download status=%d body=%q", resp.StatusCode, string(body))
	}
	if got := resp.Header.Get("Content-Disposition"); got != "" {
		t.Fatalf("content endpoint content-disposition = %q", got)
	}

	downloadResp, err := http.Get(app.URL + "/api/file/" + url.PathEscape("uploads/test/hello.txt") + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer downloadResp.Body.Close()
	if got := downloadResp.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "hello.txt") {
		t.Fatalf("download endpoint content-disposition = %q", got)
	}
}

func TestSharedKeyDownload(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{"uploads/test/hello.txt": []byte("hello")}}
	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		BackendBasePath:         "/internal",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1024,
		AllowFrontendFileAccess: true,
		EnableSharedKey:         true,
		SharedKeyBits:           128,
		SharedKeyPrefix:         ".streamuploader/shared/",
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp := postJSON(t, app.URL+"/internal/file/shared-keys", `{"object_key":"uploads/test/hello.txt","file_name":"hello.txt"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("shared key status = %d", resp.StatusCode)
	}
	var created struct {
		DownloadURL string `json:"download_url"`
	}
	decode(t, resp, &created)
	store.mu.Lock()
	markerCount := 0
	for key := range store.objects {
		if strings.HasPrefix(key, "uploads/test/.shared/") {
			markerCount++
		}
	}
	store.mu.Unlock()
	if markerCount != 1 {
		t.Fatalf("marker count = %d", markerCount)
	}
	downloadPath := strings.TrimPrefix(created.DownloadURL, "http://example.test")
	if !strings.HasSuffix(downloadPath, "/download") {
		t.Fatalf("shared download url = %q", created.DownloadURL)
	}
	downloadResp, err := http.Get(app.URL + downloadPath)
	if err != nil {
		t.Fatal(err)
	}
	defer downloadResp.Body.Close()
	body, _ := io.ReadAll(downloadResp.Body)
	if downloadResp.StatusCode != http.StatusOK || string(body) != "hello" {
		t.Fatalf("shared download status=%d body=%q", downloadResp.StatusCode, string(body))
	}
	if got := downloadResp.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "hello.txt") {
		t.Fatalf("shared download content-disposition = %q", got)
	}
}

func TestObjectDeleteCascadesSharedKeys(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{"uploads/test/hello.txt": []byte("hello")}}
	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		BackendBasePath:         "/internal",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1024,
		AllowFrontendFileAccess: true,
		EnableSharedKey:         true,
		SharedKeyBits:           128,
		SharedKeyPrefix:         ".streamuploader/shared/",
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	var created []struct {
		SharedKey string `json:"shared_key"`
	}
	for i := 0; i < 2; i++ {
		resp := postJSON(t, app.URL+"/internal/file/shared-keys", `{"object_key":"uploads/test/hello.txt","file_name":"hello.txt","created_by":"tester"}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("shared key status = %d", resp.StatusCode)
		}
		var out struct {
			SharedKey string `json:"shared_key"`
		}
		decode(t, resp, &out)
		created = append(created, out)
	}

	req, _ := http.NewRequest(http.MethodDelete, app.URL+"/internal/objects/"+url.PathEscape("uploads/test/hello.txt"), nil)
	resp := do(t, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete object status = %d", resp.StatusCode)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.objects["uploads/test/hello.txt"]; ok {
		t.Fatal("target object still exists")
	}
	for _, out := range created {
		if _, ok := store.objects[".streamuploader/shared/"+out.SharedKey]; ok {
			t.Fatalf("global shared key still exists: %s", out.SharedKey)
		}
		if _, ok := store.objects["uploads/test/.shared/"+out.SharedKey]; ok {
			t.Fatalf("marker shared key still exists: %s", out.SharedKey)
		}
	}
}

func TestDeleteSharedKeyDeletesMarker(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{"uploads/test/hello.txt": []byte("hello")}}
	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		BackendBasePath:         "/internal",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1024,
		AllowFrontendFileAccess: true,
		EnableSharedKey:         true,
		SharedKeyBits:           128,
		SharedKeyPrefix:         ".streamuploader/shared/",
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp := postJSON(t, app.URL+"/internal/file/shared-keys", `{"object_key":"uploads/test/hello.txt","file_name":"hello.txt"}`)
	var out struct {
		SharedKey string `json:"shared_key"`
	}
	decode(t, resp, &out)
	req, _ := http.NewRequest(http.MethodDelete, app.URL+"/internal/file/shared-keys/"+url.PathEscape(out.SharedKey), nil)
	deleteResp := do(t, req)
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete shared key status = %d", deleteResp.StatusCode)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.objects[".streamuploader/shared/"+out.SharedKey]; ok {
		t.Fatal("global shared key still exists")
	}
	if _, ok := store.objects["uploads/test/.shared/"+out.SharedKey]; ok {
		t.Fatal("marker shared key still exists")
	}
	if _, ok := store.objects["uploads/test/hello.txt"]; !ok {
		t.Fatal("target object was deleted")
	}
}

func TestZipArchiveDownload(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{
		"uploads/test/a.txt": []byte("alpha"),
		"uploads/test/b.txt": []byte("bravo"),
	}}
	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1024,
		AllowFrontendFileAccess: true,
		MaxArchiveFiles:         10,
		MaxArchiveBytes:         1024,
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	resp, err := http.Get(app.URL + "/api/files/" + url.PathEscape("uploads/test/a.txt") + "," + url.PathEscape("uploads/test/b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zip status = %d body=%q", resp.StatusCode, string(body))
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[file.Name] = string(content)
	}
	if got["a.txt"] != "alpha" || got["b.txt"] != "bravo" {
		t.Fatalf("zip content = %+v", got)
	}
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return do(t, req)
}

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, value any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(value); err != nil {
		t.Fatal(err)
	}
}

func normalizeTestSecurityPolicy(policy config.SecurityPolicy) config.SecurityPolicy {
	policy.MimeMagic.ExpandedAllowMIMETypes = nil
	for value, enabled := range policy.MimeMagic.AllowMIMETypes {
		if enabled {
			policy.MimeMagic.ExpandedAllowMIMETypes = append(policy.MimeMagic.ExpandedAllowMIMETypes, value)
		}
	}
	for value, enabled := range policy.MimeMagic.AllowFileTypes {
		if enabled {
			policy.MimeMagic.ExpandedAllowMIMETypes = append(policy.MimeMagic.ExpandedAllowMIMETypes, config.MIMEFileType(value)...)
		}
	}
	policy.MimeMagic.ExpandedDenyMIMETypes = nil
	for value, enabled := range policy.MimeMagic.DenyMIMETypes {
		if enabled {
			policy.MimeMagic.ExpandedDenyMIMETypes = append(policy.MimeMagic.ExpandedDenyMIMETypes, value)
		}
	}
	for value, enabled := range policy.MimeMagic.DenyFileTypes {
		if enabled {
			policy.MimeMagic.ExpandedDenyMIMETypes = append(policy.MimeMagic.ExpandedDenyMIMETypes, config.MIMEFileType(value)...)
		}
	}
	return policy
}

type uploadKeyResponse struct {
	UploadKey string `json:"upload_key"`
	ObjectKey string `json:"object_key"`
}

func testUploadConfig(security config.SecurityPolicy) config.Config {
	return config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1 << 20,
		Security:       security,
	}
}

func uploadTestObject(t *testing.T, baseURL, fileName, contentType string, body []byte) (*http.Response, uploadKeyResponse) {
	t.Helper()
	keyResp := postJSON(t, baseURL+"/api/upload/keys", fmt.Sprintf(`{"file_name":%q,"content_type":%q}`, fileName, contentType))
	if keyResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(keyResp.Body)
		_ = keyResp.Body.Close()
		t.Fatalf("create key status = %d body=%q", keyResp.StatusCode, string(respBody))
	}
	var key uploadKeyResponse
	decode(t, keyResp, &key)
	uploadReq, _ := http.NewRequest(http.MethodPut, baseURL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(body))
	uploadReq.Header.Set("Content-Type", contentType)
	return do(t, uploadReq), key
}

func makeZip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZstd(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeBrotli(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	if _, err := bw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := bw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func startFakeClamAV(t *testing.T, response string) (string, <-chan []byte, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	scanned := make(chan []byte, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		command := make([]byte, len("zINSTREAM\x00"))
		if _, err := io.ReadFull(conn, command); err != nil {
			return
		}
		var body bytes.Buffer
		var sizePrefix [4]byte
		for {
			if _, err := io.ReadFull(conn, sizePrefix[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(sizePrefix[:])
			if n == 0 {
				break
			}
			chunk := make([]byte, n)
			if _, err := io.ReadFull(conn, chunk); err != nil {
				return
			}
			_, _ = body.Write(chunk)
		}
		scanned <- body.Bytes()
		_, _ = conn.Write([]byte(response))
	}()
	closeFn := func() {
		_ = ln.Close()
		<-done
	}
	return ln.Addr().String(), scanned, closeFn
}

func assertNoTmpObjects(t *testing.T, store *fakeStore) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for key := range store.objects {
		if strings.Contains(key, "/.tmp/") {
			t.Fatalf("temporary object was not deleted: %s", key)
		}
	}
}
