package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"streamuploader/internal/config"
	"streamuploader/internal/storage"
)

type fakeStore struct {
	mu       sync.Mutex
	objects  map[string][]byte
	metadata map[string]map[string]string
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
	s.metadata[input.Key] = input.Metadata
	return storage.PutResult{ETag: `"fake"`}, nil
}

func (s *fakeStore) GetObject(_ context.Context, input storage.GetInput) (storage.GetResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[input.Key]
	if !ok {
		return storage.GetResult{}, errFakeNotFound
	}
	return storage.GetResult{
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentType:   "application/octet-stream",
		ContentLength: int64(len(body)),
		ETag:          `"fake"`,
		Metadata:      s.metadata[input.Key],
	}, nil
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
	return nil
}

var errFakeNotFound = errors.New("not found")

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
