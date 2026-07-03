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
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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

func TestCancelUploadKeyRequiresOwnerCookie(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"http://app.test"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 1024,
		UploadDeadlines: config.UploadDeadlinePolicy{
			Enabled:       true,
			MarkerPrefix:  ".uploading/",
			StartTimeout:  time.Minute,
			FinishTimeout: time.Minute,
		},
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"hello.txt"}`)
	if keyResp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d", keyResp.StatusCode)
	}
	cookies := keyResp.Cookies()
	var key struct {
		UploadKey string `json:"upload_key"`
	}
	decode(t, keyResp, &key)
	secondKeyReq, _ := http.NewRequest(http.MethodPost, app.URL+"/api/upload/keys", strings.NewReader(`{"file_name":"second.txt"}`))
	secondKeyReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		secondKeyReq.AddCookie(cookie)
	}
	secondKeyResp := do(t, secondKeyReq)
	if secondKeyResp.StatusCode != http.StatusCreated {
		t.Fatalf("second create key status = %d", secondKeyResp.StatusCode)
	}
	if got := len(secondKeyResp.Cookies()); got != 0 {
		t.Fatalf("existing owner cookie was reissued: cookies=%d", got)
	}
	_ = secondKeyResp.Body.Close()

	noCookieReq, _ := http.NewRequest(http.MethodDelete, app.URL+"/api/upload/keys/"+key.UploadKey, nil)
	noCookieResp := do(t, noCookieReq)
	if noCookieResp.StatusCode != http.StatusForbidden {
		t.Fatalf("cancel without cookie status = %d", noCookieResp.StatusCode)
	}
	_ = noCookieResp.Body.Close()

	cancelReq, _ := http.NewRequest(http.MethodDelete, app.URL+"/api/upload/keys/"+key.UploadKey, nil)
	for _, cookie := range cookies {
		cancelReq.AddCookie(cookie)
	}
	cancelResp := do(t, cancelReq)
	if cancelResp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status = %d", cancelResp.StatusCode)
	}
	_ = cancelResp.Body.Close()

	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewBufferString("hello"))
	for _, cookie := range cookies {
		uploadReq.AddCookie(cookie)
	}
	uploadResp := do(t, uploadReq)
	if uploadResp.StatusCode != http.StatusGone {
		t.Fatalf("upload after cancel status = %d", uploadResp.StatusCode)
	}
	_ = uploadResp.Body.Close()
}

func TestBackendWaitAsyncTasks(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:            "standalone_cross_origin",
		PublicBaseURL:   "http://example.test",
		BackendBasePath: "/internal",
		AllowedOrigins:  []string{"*"},
		Bucket:          "bucket",
		SessionTTL:      time.Hour,
	}
	srv := New(cfg, store)
	app := httptest.NewServer(srv.Handler())
	defer app.Close()

	objectKey := "uploads/key/image.png"
	if err := srv.putAsyncTaskMarker(context.Background(), asyncTaskMarker{
		ObjectKey: objectKey,
		Kind:      "image_thumbnail",
		Status:    "running",
	}); err != nil {
		t.Fatal(err)
	}

	waitReq, _ := http.NewRequest(http.MethodGet, app.URL+"/internal/tasks/wait?object_key="+url.QueryEscape(objectKey)+"&timeout_seconds=1&poll_millis=50", nil)
	waitResp := do(t, waitReq)
	if waitResp.StatusCode != http.StatusOK {
		t.Fatalf("wait status = %d", waitResp.StatusCode)
	}
	var timedOut struct {
		Ready   bool `json:"ready"`
		Timeout bool `json:"timeout"`
		Tasks   []struct {
			Pending bool `json:"pending"`
		} `json:"tasks"`
	}
	decode(t, waitResp, &timedOut)
	if timedOut.Ready || !timedOut.Timeout || len(timedOut.Tasks) != 1 || !timedOut.Tasks[0].Pending {
		t.Fatalf("wait timeout response = %+v", timedOut)
	}

	srv.deleteAsyncTaskMarker(context.Background(), objectKey, "image_thumbnail")
	doneReq, _ := http.NewRequest(http.MethodGet, app.URL+"/internal/tasks/wait?object_key="+url.QueryEscape(objectKey)+"&timeout_seconds=1", nil)
	doneResp := do(t, doneReq)
	if doneResp.StatusCode != http.StatusOK {
		t.Fatalf("done wait status = %d", doneResp.StatusCode)
	}
	var done struct {
		Ready   bool `json:"ready"`
		Timeout bool `json:"timeout"`
		Tasks   []struct {
			Pending bool `json:"pending"`
		} `json:"tasks"`
	}
	decode(t, doneResp, &done)
	if !done.Ready || done.Timeout || len(done.Tasks) != 1 || done.Tasks[0].Pending {
		t.Fatalf("wait done response = %+v", done)
	}

	postResp := postJSON(t, app.URL+"/internal/tasks/wait", fmt.Sprintf(`{"object_keys":[%q]}`, objectKey))
	if postResp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST wait status = %d", postResp.StatusCode)
	}
	_ = postResp.Body.Close()
}

func TestUploadImageGeneratesThumbnail(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1 << 20,
		AllowFrontendFileAccess: true,
		Thumbnails: config.ThumbnailPolicy{
			Enabled:         true,
			ExecutionMode:   "sequential",
			Width:           64,
			Height:          64,
			Fit:             "contain",
			LosslessPolicy:  "force_avif_reduction",
			PreferredFormat: "avif",
			ObjectKeySuffix: "/thumbnail",
		},
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"image.png","content_type":"image/png"}`)
	if keyResp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d", keyResp.StatusCode)
	}
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	var pngBody bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 96, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 4), B: 180, A: 255})
		}
	}
	if err := png.Encode(&pngBody, img); err != nil {
		t.Fatal(err)
	}
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(pngBody.Bytes()))
	uploadReq.Header.Set("Content-Type", "image/png")
	uploadResp := do(t, uploadReq)
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", uploadResp.StatusCode)
	}
	var uploaded struct {
		Thumbnail *struct {
			Status    string `json:"status"`
			ObjectKey string `json:"object_key"`
		} `json:"thumbnail"`
	}
	decode(t, uploadResp, &uploaded)
	if uploaded.Thumbnail == nil || uploaded.Thumbnail.Status != "generated" || uploaded.Thumbnail.ObjectKey != key.ObjectKey+"/thumbnail" {
		t.Fatalf("thumbnail response = %+v", uploaded.Thumbnail)
	}
	if _, ok := store.objects[key.ObjectKey+"/thumbnail"]; !ok {
		t.Fatalf("thumbnail object was not stored")
	}
	thumbResp, err := http.Get(app.URL + "/api/file/" + url.PathEscape(key.ObjectKey) + "/thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	defer thumbResp.Body.Close()
	if thumbResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(thumbResp.Body)
		t.Fatalf("thumbnail status = %d body=%q", thumbResp.StatusCode, string(body))
	}
	body, err := io.ReadAll(thumbResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("thumbnail response body is empty")
	}
}

func TestUploadImageGeneratesThumbnailWithExternalWebhook(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	var webhookBody []byte
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		webhookBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read webhook body: %v", err)
			return
		}
		if r.Header.Get("X-Thumbnail-Width") != "32" || r.Header.Get("X-Thumbnail-Preferred-Format") != "webp" {
			t.Errorf("unexpected thumbnail headers: width=%q format=%q", r.Header.Get("X-Thumbnail-Width"), r.Header.Get("X-Thumbnail-Preferred-Format"))
		}
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("X-Thumbnail-Width", "32")
		w.Header().Set("X-Thumbnail-Height", "24")
		w.Header().Set("X-Thumbnail-Backend", "test-webhook")
		_, _ = w.Write([]byte("converted-thumbnail"))
	}))
	defer converter.Close()

	cfg := config.Config{
		Mode:                    "standalone_cross_origin",
		PublicBaseURL:           "http://example.test",
		UploadBasePath:          "/api/upload",
		AllowedOrigins:          []string{"*"},
		Bucket:                  "bucket",
		SessionTTL:              time.Hour,
		MaxUploadBytes:          1 << 20,
		AllowFrontendFileAccess: true,
		Thumbnails: config.ThumbnailPolicy{
			Enabled:            true,
			ExecutionMode:      "sequential",
			Width:              32,
			Height:             24,
			Fit:                "contain",
			LosslessPolicy:     "force_avif_reduction",
			PreferredFormat:    "webp",
			ObjectKeySuffix:    "/thumbnail",
			ExternalWebhookURL: converter.URL,
			ExternalTimeout:    time.Second,
		},
	}
	app := httptest.NewServer(New(cfg, store).Handler())
	defer app.Close()

	keyResp := postJSON(t, app.URL+"/api/upload/keys", `{"file_name":"image.png","content_type":"image/png"}`)
	if keyResp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d", keyResp.StatusCode)
	}
	var key struct {
		UploadKey string `json:"upload_key"`
		ObjectKey string `json:"object_key"`
	}
	decode(t, keyResp, &key)

	var pngBody bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	if err := png.Encode(&pngBody, img); err != nil {
		t.Fatal(err)
	}
	uploadReq, _ := http.NewRequest(http.MethodPut, app.URL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(pngBody.Bytes()))
	uploadReq.Header.Set("Content-Type", "image/png")
	uploadResp := do(t, uploadReq)
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", uploadResp.StatusCode)
	}
	var uploaded struct {
		Thumbnail *struct {
			Status      string `json:"status"`
			ObjectKey   string `json:"object_key"`
			ContentType string `json:"content_type"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
		} `json:"thumbnail"`
	}
	decode(t, uploadResp, &uploaded)
	if uploaded.Thumbnail == nil || uploaded.Thumbnail.Status != "generated" || uploaded.Thumbnail.ContentType != "image/webp" {
		t.Fatalf("thumbnail response = %+v", uploaded.Thumbnail)
	}
	if uploaded.Thumbnail.Width != 32 || uploaded.Thumbnail.Height != 24 {
		t.Fatalf("thumbnail size = %+v", uploaded.Thumbnail)
	}
	if !bytes.Equal(webhookBody, pngBody.Bytes()) {
		t.Fatalf("webhook body length = %d, want %d", len(webhookBody), pngBody.Len())
	}
	if got := string(store.objects[key.ObjectKey+"/thumbnail"]); got != "converted-thumbnail" {
		t.Fatalf("stored thumbnail = %q", got)
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

func TestUploadMimeMagicCheckAlwaysRejectsMismatch(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.FileSanitization.Enabled = false
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
	if uploadResp.StatusCode != http.StatusUnsupportedMediaType {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d body=%q", uploadResp.StatusCode, string(body))
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("mismatched upload was stored")
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

	png := makeTestPNG(t, 1, 1)
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

func TestUploadSanitizesJPEGEXIFBeforeStorage(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := testUploadConfig(config.DefaultSecurityPolicy())
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	body := makeJPEGWithAPP1(t)
	if !jpegHasAPP1(body) {
		t.Fatal("test JPEG is missing APP1 metadata")
	}
	resp, key := uploadTestObject(t, srv.URL, "photo.jpg", "image/jpeg", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	store.mu.Lock()
	got := append([]byte(nil), store.objects[key.ObjectKey]...)
	store.mu.Unlock()
	if jpegHasAPP1(got) {
		t.Fatal("stored JPEG still contains APP1 metadata")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(got)); err != nil {
		t.Fatalf("stored JPEG is not decodable: %v", err)
	}
}

func TestUploadSanitizesQuickTimeMetadataBeforeStorage(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	cfg := testUploadConfig(security)
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	body := makeBMFF(
		bmffBox("ftyp", []byte("qt  \x00\x00\x00\x00qt  ")),
		bmffBox("moov", append(
			bmffBox("mvhd", []byte{0, 0, 0, 0}),
			bmffBox("udta", []byte("GPS and camera metadata"))...,
		)),
		bmffBox("mdat", []byte("media payload")),
	)
	resp, key := uploadTestObject(t, srv.URL, "clip.mov", "video/quicktime", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	store.mu.Lock()
	got := append([]byte(nil), store.objects[key.ObjectKey]...)
	store.mu.Unlock()
	if bytes.Contains(got, []byte("GPS and camera metadata")) || bytes.Contains(got, []byte("udta")) {
		t.Fatalf("stored video still contains metadata box: %q", string(got))
	}
	if !bytes.Contains(got, []byte("media payload")) {
		t.Fatal("stored video lost media payload")
	}
}

func TestSanitizeWEBPMetadataPreservesICC(t *testing.T) {
	body := makeWEBP(
		webpChunk("VP8X", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}),
		webpChunk("ICCP", []byte("profile")),
		webpChunk("EXIF", []byte("gps")),
		webpChunk("XMP ", []byte("author")),
	)
	got, err := sanitizeWEBPMetadata(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("gps")) || bytes.Contains(got, []byte("author")) || bytes.Contains(got, []byte("EXIF")) || bytes.Contains(got, []byte("XMP ")) {
		t.Fatalf("WebP metadata was not removed: %q", string(got))
	}
	if !bytes.Contains(got, []byte("ICCP")) || !bytes.Contains(got, []byte("profile")) {
		t.Fatalf("WebP ICC profile was not preserved: %q", string(got))
	}
}

func TestUploadRejectsSVGActiveContent(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	cfg := testUploadConfig(security)
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, key := uploadTestObject(t, srv.URL, "bad.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script></svg>`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["error"] != "svg_active_content_rejected" && body["error"] != "svg_script_detected" {
		t.Fatalf("error body = %+v", body)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected SVG was stored")
	}
}

func TestInspectMarkupRejectsMarkdownHTMLScript(t *testing.T) {
	policy := config.DefaultSecurityPolicy()
	err := inspectMarkup([]byte("# Title\n\n<script>alert(1)</script>\n"), "text/markdown", "note.md", policy.ResourceLimits, policy.FileSanitization.Markup)
	if err == nil {
		t.Fatal("expected Markdown raw HTML script to be rejected")
	}
	var uploadErr securityUploadError
	if !errors.As(err, &uploadErr) || uploadErr.code != "markup_script_detected" {
		t.Fatalf("error = %#v", err)
	}
}

func TestInspectMarkupRejectsHTMLScript(t *testing.T) {
	policy := config.DefaultSecurityPolicy()
	err := inspectMarkup([]byte(`<html><script>alert(1)</script></html>`), "text/html", "page.html", policy.ResourceLimits, policy.FileSanitization.Markup)
	if err == nil {
		t.Fatal("expected HTML script to be rejected")
	}
	var uploadErr securityUploadError
	if !errors.As(err, &uploadErr) || uploadErr.code != "markup_script_detected" {
		t.Fatalf("error = %#v", err)
	}
}

func TestInspectMarkupRejectsXMLExternalEntity(t *testing.T) {
	policy := config.DefaultSecurityPolicy()
	body := []byte(`<?xml version="1.0"?><!DOCTYPE doc [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><doc>&xxe;</doc>`)
	err := inspectMarkup(body, "application/xml", "doc.xml", policy.ResourceLimits, policy.FileSanitization.Markup)
	if err == nil {
		t.Fatal("expected XML external entity to be rejected")
	}
	var uploadErr securityUploadError
	if !errors.As(err, &uploadErr) || uploadErr.code != "xml_external_entity_detected" {
		t.Fatalf("error = %#v", err)
	}
}

func TestUploadRejectsHTMLIframe(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := testUploadConfig(config.DefaultSecurityPolicy())
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, key := uploadTestObject(t, srv.URL, "bad.html", "text/html", []byte(`<html><body><iframe src="https://example.test"></iframe></body></html>`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["error"] != "markup_iframe_detected" {
		t.Fatalf("error body = %+v", body)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected HTML was stored")
	}
}

func TestUploadRejectsRTFByDefaultAndAllowsOptOut(t *testing.T) {
	body := []byte(`{\rtf1\ansi{\object\objemb{\*\objdata 01050000}}}`)
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := testUploadConfig(config.DefaultSecurityPolicy())
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, key := uploadTestObject(t, srv.URL, "doc.rtf", "application/rtf", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var errorBody map[string]string
	decode(t, resp, &errorBody)
	if errorBody["error"] != "legacy_document_rejected" {
		t.Fatalf("error body = %+v", errorBody)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected RTF was stored")
	}

	optOutStore := &fakeStore{objects: map[string][]byte{}}
	optOut := config.DefaultSecurityPolicy()
	optOut.FileSanitization.PerFileType = map[string]config.FileTypePolicy{"application/rtf": {Mode: "accept_as_is"}}
	optOutCfg := testUploadConfig(optOut)
	optOutSrv := httptest.NewServer(New(optOutCfg, optOutStore).Handler())
	defer optOutSrv.Close()

	okResp, okKey := uploadTestObject(t, optOutSrv.URL, "doc.rtf", "application/rtf", body)
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(okResp.Body)
		t.Fatalf("opt-out upload status = %d body=%q", okResp.StatusCode, string(respBody))
	}
	optOutStore.mu.Lock()
	got := optOutStore.objects[okKey.ObjectKey]
	optOutStore.mu.Unlock()
	if !bytes.Equal(got, body) {
		t.Fatal("opt-out RTF body changed")
	}
}

func TestUploadRejectsPDFJavaScript(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := testUploadConfig(config.DefaultSecurityPolicy())
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog /OpenAction << /S /JavaScript /JS (app.alert('x')) >> >>\nendobj\n%%EOF\n")
	resp, key := uploadTestObject(t, srv.URL, "active.pdf", "application/pdf", pdf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["error"] != "document_active_content_rejected" {
		t.Fatalf("error body = %+v", body)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected PDF was stored")
	}
}

func TestUploadPDFScansTmpThenPublishes(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := testUploadConfig(config.DefaultSecurityPolicy())
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n2 0 obj\n<< /Type /Page >>\nendobj\n%%EOF\n")
	resp, key := uploadTestObject(t, srv.URL, "safe.pdf", "application/pdf", pdf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	store.mu.Lock()
	got := append([]byte(nil), store.objects[key.ObjectKey]...)
	copies := store.copies
	store.mu.Unlock()
	if !bytes.Equal(got, pdf) {
		t.Fatal("published PDF body changed")
	}
	if copies != 1 {
		t.Fatalf("PDF full scan should publish from tmp via copy, copies=%d", copies)
	}
	assertNoTmpObjects(t, store)
}

func TestUploadRejectsOfficeMacroPart(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	cfg := testUploadConfig(security)
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	body := makeZipWithEntries(t, map[string][]byte{
		"[Content_Types].xml":          []byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`),
		"word/document.xml":            []byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`),
		"word/vbaProject.bin":          []byte("macro"),
		"word/_rels/document.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`),
	})
	resp, key := uploadTestObject(t, srv.URL, "macro.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var errorBody map[string]string
	decode(t, resp, &errorBody)
	if errorBody["error"] != "document_active_content_rejected" {
		t.Fatalf("error body = %+v", errorBody)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected Office document was stored")
	}
}

func TestUploadRejectsLegacyOfficeByDefaultAndAllowsOptOut(t *testing.T) {
	body := []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 'd', 'o', 'c'}
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	cfg := testUploadConfig(security)
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, key := uploadTestObject(t, srv.URL, "legacy.doc", "application/msword", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var errorBody map[string]string
	decode(t, resp, &errorBody)
	if errorBody["error"] != "legacy_office_rejected" {
		t.Fatalf("error body = %+v", errorBody)
	}
	store.mu.Lock()
	_, stored := store.objects[key.ObjectKey]
	store.mu.Unlock()
	if stored {
		t.Fatal("rejected legacy Office document was stored")
	}

	optOutStore := &fakeStore{objects: map[string][]byte{}}
	optOut := config.DefaultSecurityPolicy()
	optOut.FileSanitization.PerFileType = map[string]config.FileTypePolicy{"application/msword": {Mode: "accept_as_is"}}
	optOutCfg := testUploadConfig(optOut)
	optOutSrv := httptest.NewServer(New(optOutCfg, optOutStore).Handler())
	defer optOutSrv.Close()

	okResp, okKey := uploadTestObject(t, optOutSrv.URL, "legacy.doc", "application/msword", body)
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(okResp.Body)
		t.Fatalf("opt-out upload status = %d body=%q", okResp.StatusCode, string(respBody))
	}
	optOutStore.mu.Lock()
	got := optOutStore.objects[okKey.ObjectKey]
	optOutStore.mu.Unlock()
	if !bytes.Equal(got, body) {
		t.Fatal("opt-out legacy Office body changed")
	}
}

func TestUploadRejectsImageDimensionLimit(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	security := config.DefaultSecurityPolicy()
	security.ResourceLimits.MaxImageWidth = 1
	cfg := testUploadConfig(security)
	srv := httptest.NewServer(New(cfg, store).Handler())
	defer srv.Close()

	resp, _ := uploadTestObject(t, srv.URL, "wide.png", "image/png", makeTestPNG(t, 2, 1))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d body=%q", resp.StatusCode, string(respBody))
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["error"] != "resource_limit_exceeded" {
		t.Fatalf("error body = %+v", body)
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
	return makeZipWithEntries(t, map[string][]byte{name: body})
}

func makeZipWithEntries(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeJPEGWithAPP1(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 40, B: 80, A: 255})
		}
	}
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	body := buf.Bytes()
	if len(body) < 2 || body[0] != 0xff || body[1] != 0xd8 {
		t.Fatal("encoded JPEG missing SOI")
	}
	app1Payload := append([]byte("Exif\x00\x00"), []byte("GPS metadata")...)
	segmentLen := len(app1Payload) + 2
	app1 := []byte{0xff, 0xe1, byte(segmentLen >> 8), byte(segmentLen)}
	app1 = append(app1, app1Payload...)
	out := append([]byte{}, body[:2]...)
	out = append(out, app1...)
	out = append(out, body[2:]...)
	return out
}

func makeBMFF(boxes ...[]byte) []byte {
	return bytes.Join(boxes, nil)
}

func bmffBox(boxType string, payload []byte) []byte {
	var out bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)+8))
	copy(header[4:8], []byte(boxType))
	out.Write(header[:])
	out.Write(payload)
	return out.Bytes()
}

func makeWEBP(chunks ...[]byte) []byte {
	payload := bytes.Join(chunks, nil)
	out := make([]byte, 12)
	copy(out[:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(out[4:8], uint32(4+len(payload)))
	copy(out[8:12], []byte("WEBP"))
	out = append(out, payload...)
	return out
}

func webpChunk(chunkType string, payload []byte) []byte {
	var out bytes.Buffer
	var header [8]byte
	copy(header[:4], []byte(chunkType))
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(payload)))
	out.Write(header[:])
	out.Write(payload)
	if len(payload)%2 == 1 {
		out.WriteByte(0)
	}
	return out.Bytes()
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
