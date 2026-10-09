package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"streamuploader/internal/config"
	"streamuploader/internal/model"
)

const (
	testDocxType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	testPptxType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	testDocPass  = "パスワード🔑bdf"
)

func documentFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("../docpreview/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func documentTestConfig(mutate func(*config.Config)) config.Config {
	cfg := testUploadConfig(config.DefaultSecurityPolicy())
	cfg.Thumbnails = config.DefaultSecurityPolicy().Thumbnails // image thumbnails stay disabled
	cfg.TextExtraction = config.DefaultSecurityPolicy().TextExtraction
	cfg.DocumentProcessing = config.DefaultSecurityPolicy().DocumentProcessing
	cfg.DocumentProcessing.ExecutionMode = "sequential"
	cfg.Thumbnails.PreferredFormat = "jpeg"
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

func newUploadKey(t *testing.T, baseURL, fileName, contentType string) uploadKeyResponse {
	t.Helper()
	resp := postJSON(t, baseURL+"/api/upload/keys", fmt.Sprintf(`{"file_name":%q,"content_type":%q}`, fileName, contentType))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key status = %d", resp.StatusCode)
	}
	var key uploadKeyResponse
	decode(t, resp, &key)
	return key
}

func putDocument(t *testing.T, baseURL string, key uploadKeyResponse, contentType string, body []byte, password string) (*http.Response, map[string]any, *model.UploadItem) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, baseURL+"/api/upload/keys/"+key.UploadKey+"/content", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if password != "" {
		req.Header.Set(documentPasswordHeader, password)
	}
	resp := do(t, req)
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	var item model.UploadItem
	_ = json.Unmarshal(raw, &item)
	return resp, out, &item
}

func storedKeys(store *fakeStore) []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	var keys []string
	for k := range store.objects {
		if strings.HasPrefix(k, ".uploading/") {
			continue // upload deadline markers
		}
		keys = append(keys, k)
	}
	return keys
}

func TestDocumentUploadAlwaysProducesThumbnailTextAndBDF(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	srv := newTestServer(t, New(documentTestConfig(nil), store).Handler())
	defer srv.Close()

	key := newUploadKey(t, srv.URL, "report.docx", testDocxType)
	resp, body, item := putDocument(t, srv.URL, key, testDocxType, documentFixture(t, "sample.docx"), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d %v", resp.StatusCode, body)
	}
	if item.Thumbnail == nil || item.Thumbnail.Status != "generated" {
		t.Fatalf("thumbnail = %+v", item.Thumbnail)
	}
	if item.ExtractedContent == nil || item.ExtractedContent.Status != "generated" {
		t.Fatalf("extracted = %+v", item.ExtractedContent)
	}
	if item.Preview == nil || item.Preview.Status != "generated" || item.Preview.ContentType != "application/x-bdf" {
		t.Fatalf("preview = %+v", item.Preview)
	}
	if item.Protected {
		t.Fatal("plain document marked protected")
	}
	store.mu.Lock()
	_, hasBDF := store.objects[key.ObjectKey+".bdf"]
	_, hasThumb := store.objects[key.ObjectKey+"/thumbnail"]
	_, hasText := store.objects[key.ObjectKey+".text.json"]
	store.mu.Unlock()
	if !hasBDF || !hasThumb || !hasText {
		t.Fatalf("stored keys = %v", storedKeys(store))
	}
	store.mu.Lock()
	text := string(store.objects[key.ObjectKey+".text.json"])
	store.mu.Unlock()
	if !strings.Contains(text, `"pages"`) {
		t.Fatalf("extracted content has no per-page text: %s", text)
	}
	if !strings.Contains(text, `"extracted"`) || !strings.Contains(text, `"backend": "bdf"`) {
		t.Fatalf("extracted content = %s", text)
	}
	assertNoTmpObjects(t, store)
}

func TestProtectedDocumentNeedsPasswordThenGetsLockMark(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	srv := newTestServer(t, New(documentTestConfig(nil), store).Handler())
	defer srv.Close()
	input := documentFixture(t, "protected.pptx")

	key := newUploadKey(t, srv.URL, "secret.pptx", testPptxType)
	resp, body, _ := putDocument(t, srv.URL, key, testPptxType, input, "")
	if resp.StatusCode != http.StatusUnprocessableEntity || body["code"] != "document_password_required" {
		t.Fatalf("no password: %d %v", resp.StatusCode, body)
	}
	resp, body, _ = putDocument(t, srv.URL, key, testPptxType, input, "wrong")
	if resp.StatusCode != http.StatusUnprocessableEntity || body["code"] != "document_password_invalid" {
		t.Fatalf("wrong password: %d %v", resp.StatusCode, body)
	}
	if keys := storedKeys(store); len(keys) != 0 {
		t.Fatalf("rejected upload left objects: %v", keys)
	}
	var item *model.UploadItem
	resp, body, item = putDocument(t, srv.URL, key, testPptxType, input, testDocPass)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("right password: %d %v", resp.StatusCode, body)
	}
	if !item.Protected || item.Thumbnail.Status != "locked" || item.ExtractedContent.Status != "locked" {
		t.Fatalf("protected item = %+v thumb=%+v text=%+v", item, item.Thumbnail, item.ExtractedContent)
	}
	if item.Preview == nil || item.Preview.Status != "generated" {
		t.Fatalf("preview = %+v", item.Preview)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.objects[key.ObjectKey+"/thumbnail"]; ok {
		t.Fatal("protected document must not have a thumbnail")
	}
	if _, ok := store.objects[key.ObjectKey+".text.json"]; ok {
		t.Fatal("protected document must not have search text")
	}
	if bytes.Contains(store.objects[key.ObjectKey+".bdf"], []byte(testDocPass)) {
		t.Fatal("password leaked into bdf")
	}
}

func TestDocumentConversionFailureFlag(t *testing.T) {
	broken := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n2 0 obj\n<< /Type /Page >>\nendobj\n%%EOF\n")

	t.Run("flag off keeps upload and marks assets failed", func(t *testing.T) {
		store := &fakeStore{objects: map[string][]byte{}}
		srv := newTestServer(t, New(documentTestConfig(nil), store).Handler())
		defer srv.Close()
		key := newUploadKey(t, srv.URL, "broken.pdf", "application/pdf")
		resp, body, item := putDocument(t, srv.URL, key, "application/pdf", broken, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d %v", resp.StatusCode, body)
		}
		if item.Preview == nil || item.Preview.Status != "failed" || item.Thumbnail.Status != "failed" {
			t.Fatalf("assets = %+v %+v", item.Preview, item.Thumbnail)
		}
	})
	t.Run("flag on cancels upload", func(t *testing.T) {
		store := &fakeStore{objects: map[string][]byte{}}
		cfg := documentTestConfig(func(c *config.Config) { c.DocumentProcessing.FailUploadOnError = true })
		srv := newTestServer(t, New(cfg, store).Handler())
		defer srv.Close()
		key := newUploadKey(t, srv.URL, "broken.pdf", "application/pdf")
		resp, body, _ := putDocument(t, srv.URL, key, "application/pdf", broken, "")
		if resp.StatusCode != http.StatusUnprocessableEntity || body["code"] != "document_processing_failed" {
			t.Fatalf("status = %d %v", resp.StatusCode, body)
		}
		if keys := storedKeys(store); len(keys) != 0 {
			t.Fatalf("failed upload left objects: %v", keys)
		}
	})
}

func TestPreviewRouteServesBDF(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := documentTestConfig(func(c *config.Config) { c.AllowFrontendFileAccess = true })
	srv := newTestServer(t, New(cfg, store).Handler())
	defer srv.Close()
	key := newUploadKey(t, srv.URL, "report.docx", testDocxType)
	if resp, _, _ := putDocument(t, srv.URL, key, testDocxType, documentFixture(t, "sample.docx"), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp, err := http.Get(srv.URL + "/api/file/" + url.PathEscape(key.ObjectKey) + "/preview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-bdf") {
		t.Fatalf("preview = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestRealOOXMLPassesDefaultOfficePolicy(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	srv := newTestServer(t, New(documentTestConfig(nil), store).Handler())
	defer srv.Close()
	for _, tc := range []struct{ file, name, contentType string }{
		{"sample.docx", "a.docx", testDocxType},
		{"sample.xlsx", "a.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"sample.pptx", "a.pptx", testPptxType},
	} {
		key := newUploadKey(t, srv.URL, tc.name, tc.contentType)
		resp, body, _ := putDocument(t, srv.URL, key, tc.contentType, documentFixture(t, tc.file), "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d %v", tc.file, resp.StatusCode, body)
		}
	}
}

func TestPhotoshopAndIllustratorUploads(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	// Like TIFF, AVIF and HEIC, Photoshop files cannot be metadata-sanitized,
	// so the default image policy rejects them; opt in per file type.
	cfg := documentTestConfig(func(c *config.Config) {
		c.Security.FileSanitization.PerFileType = map[string]config.FileTypePolicy{"image/vnd.adobe.photoshop": {Mode: "accept_as_is"}}
	})
	srv := newTestServer(t, New(cfg, store).Handler())
	defer srv.Close()
	for _, tc := range []struct{ file, name, contentType string }{
		{"sample.psd", "art.psd", "image/vnd.adobe.photoshop"},
		{"sample.ai", "logo.ai", "application/illustrator"},
		{"sample.ai", "logo2.ai", "application/pdf"},
		{"sample.epub", "book.epub", "application/epub+zip"},
		{"sample.parquet", "data.parquet", "application/vnd.apache.parquet"},
		{"sample.vsdx", "flow.vsdx", "application/vnd.ms-visio.drawing"},
	} {
		key := newUploadKey(t, srv.URL, tc.name, tc.contentType)
		resp, body, item := putDocument(t, srv.URL, key, tc.contentType, documentFixture(t, tc.file), "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d %v", tc.name, resp.StatusCode, body)
		}
		if item.Thumbnail == nil || item.Thumbnail.Status != "generated" || item.Preview == nil || item.Preview.Status != "generated" {
			t.Fatalf("%s: thumbnail=%+v preview=%+v", tc.name, item.Thumbnail, item.Preview)
		}
	}
}

func TestTextFormatsGetPreviewAndKeepTextExtraction(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	srv := newTestServer(t, New(documentTestConfig(nil), store).Handler())
	defer srv.Close()
	csv := "name,qty\napple,3\nbanana,12\n"
	key := newUploadKey(t, srv.URL, "stock.csv", "text/csv")
	resp, body, item := putDocument(t, srv.URL, key, "text/csv", []byte(csv), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d %v", resp.StatusCode, body)
	}
	if item.Preview == nil || item.Preview.Status != "generated" || item.Thumbnail == nil || item.Thumbnail.Status != "generated" {
		t.Fatalf("preview=%+v thumbnail=%+v", item.Preview, item.Thumbnail)
	}
	store.mu.Lock()
	text := string(store.objects[key.ObjectKey+".text.json"])
	store.mu.Unlock()
	if !strings.Contains(text, `"text"`) || !strings.Contains(text, "banana") || !strings.Contains(text, `"pages"`) {
		t.Fatalf("extracted content = %s", text)
	}
}

func TestTextNotMatchingItsNameFallsBackToPlainText(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	cfg := documentTestConfig(func(c *config.Config) {
		c.TextExtraction.Enabled = true
		c.TextExtraction.ExecutionMode = "sequential"
	})
	srv := newTestServer(t, New(cfg, store).Handler())
	defer srv.Close()
	key := newUploadKey(t, srv.URL, "notes.csv", "text/csv")
	resp, body, item := putDocument(t, srv.URL, key, "text/csv", []byte("Just one sentence.\n"), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d %v", resp.StatusCode, body)
	}
	if item.Preview != nil || item.Thumbnail != nil {
		t.Fatalf("plain text must not get a preview: %+v %+v", item.Preview, item.Thumbnail)
	}
	if item.ExtractedContent == nil {
		t.Fatal("plain text keeps the normal text extraction")
	}
}

func TestBinaryFormatMismatchIsRefused(t *testing.T) {
	store := &fakeStore{objects: map[string][]byte{}}
	srv := newTestServer(t, New(documentTestConfig(nil), store).Handler())
	defer srv.Close()

	// Magic numbers alone do not make an EPUB or a Parquet file.
	var epub bytes.Buffer
	zw := zip.NewWriter(&epub)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte("application/epub+zip"))
	_ = zw.Close()
	fakeParquet := append(append([]byte("PAR1"), make([]byte, 64)...), []byte("PAR1")...)

	for _, tc := range []struct {
		name, contentType string
		body              []byte
	}{
		{"book.epub", "application/epub+zip", epub.Bytes()},
		{"data.parquet", "application/vnd.apache.parquet", fakeParquet},
	} {
		key := newUploadKey(t, srv.URL, tc.name, tc.contentType)
		resp, body, _ := putDocument(t, srv.URL, key, tc.contentType, tc.body, "")
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: %d %v", tc.name, resp.StatusCode, body)
		}
		if body["code"] != "document_format_mismatch" {
			t.Fatalf("%s: error = %v", tc.name, body["code"])
		}
	}
	// An upload of another format under a Visio name is refused too.
	key := newUploadKey(t, srv.URL, "flow.vsdx", "application/vnd.ms-visio.drawing")
	if resp, body, _ := putDocument(t, srv.URL, key, "application/vnd.ms-visio.drawing", documentFixture(t, "sample.parquet"), ""); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("parquet as visio: %d %v", resp.StatusCode, body)
	}
	if keys := storedKeys(store); len(keys) != 0 {
		t.Fatalf("refused uploads left objects: %v", keys)
	}
}
