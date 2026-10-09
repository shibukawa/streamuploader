// Package e2e runs the whole all-in-one Drive in one process against the
// in-memory object store: streamuploader's upload pipeline, the journal, the
// indexer, the tantivy sidecar and the Drive API. It checks the m1 completion
// conditions recorded in .knowledge/concepts/vision/local-drive.yaml.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"streamuploader/drive/indexer"
	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	driveserver "streamuploader/drive/server"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/snapshot"
	"streamuploader/internal/config"
	"streamuploader/internal/model"
	suserver "streamuploader/internal/server"
	"streamuploader/internal/storage"
)

const (
	docxType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	pdfType  = "application/pdf"
)

func sidecarBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("DRIVE_SEARCH_BIN"); p != "" {
		return p
	}
	for _, c := range []string{"../../search/target/release/drivesearch", "../../search/target/debug/drivesearch"} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	t.Skip("drivesearch binary not built; run `cargo build --release` in search/")
	return ""
}

type stack struct {
	t      *testing.T
	ctx    context.Context
	store  *memstore.Store
	cfg    config.Config
	search *sidecar.Client
	metas  *meta.Cache
	// ix is the in-process indexer of an all-in-one stack; follower is the
	// snapshot follower of a server-only stack.
	ix       *indexer.Indexer
	follower *snapshot.Follower
	startRep indexer.Report
	srv      *httptest.Server
	client   *http.Client
	index    string
}

// newStack builds an all-in-one stack (server plus indexer) that publishes
// its index to the bucket.
func newStack(t *testing.T, store *memstore.Store, indexDir string) *stack {
	t.Helper()
	return buildStack(t, store, indexDir, "all")
}

// newServerStack builds a server-only stack that follows the published
// snapshot with a read-only sidecar.
func newServerStack(t *testing.T, store *memstore.Store, indexDir string) *stack {
	t.Helper()
	return buildStack(t, store, indexDir, "server")
}

func buildStack(t *testing.T, store *memstore.Store, indexDir, mode string) *stack {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	security := config.DefaultSecurityPolicy()
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 8 << 20,
		Security:       security,
	}
	cfg.Thumbnails = security.Thumbnails
	cfg.Thumbnails.Enabled = true
	cfg.Thumbnails.ExecutionMode = "sequential"
	cfg.Thumbnails.PreferredFormat = "jpeg"
	cfg.TextExtraction = security.TextExtraction
	cfg.TextExtraction.ExecutionMode = "sequential"
	cfg.DocumentProcessing = security.DocumentProcessing
	cfg.DocumentProcessing.ExecutionMode = "sequential"

	search := sidecar.New(sidecarBinary(t), indexDir)
	t.Cleanup(func() { _ = search.Close() })
	jstore := &journal.Store{Objects: store, Bucket: cfg.Bucket, Prefix: "drive/"}
	metas := meta.NewCache()
	s := &stack{t: t, ctx: ctx, store: store, cfg: cfg, search: search, metas: metas, index: indexDir}
	var control driveserver.IndexerControl
	switch mode {
	case "all":
		ix := indexer.New(indexer.Config{Tenant: "t1", Bucket: cfg.Bucket, Prefix: "drive/", IndexDir: indexDir, Snapshot: true}, store, jstore, search, metas, nil)
		rep, err := ix.Start(ctx)
		if err != nil {
			t.Fatalf("indexer start: %v", err)
		}
		s.ix, s.startRep, control = ix, rep, ix
	case "server":
		snapStore := &snapshot.Store{Objects: store, Bucket: cfg.Bucket, Prefix: "drive/", Tenant: "t1"}
		follower := snapshot.NewFollower(snapStore, search, metas, indexDir, nil)
		if err := follower.Start(ctx); err != nil {
			t.Fatalf("follower start: %v", err)
		}
		s.follower, control = follower, follower
	default:
		t.Fatalf("unknown stack mode %q", mode)
	}
	uploader := suserver.New(cfg, store).Handler()
	// A negative overlay TTL disables the overlay cache, so a write through
	// one stack is visible through another at once.
	drv := driveserver.New(driveserver.Config{Tenant: "t1", Bucket: cfg.Bucket, Prefix: "drive/", OverlayTTL: -1}, driveserver.Deps{
		Store: store, Journal: jstore, Metas: metas, Search: search, Indexer: control, Uploader: uploader,
	})
	srv := httptest.NewServer(drv.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	s.srv, s.client = srv, &http.Client{Jar: jar}
	return s
}

func (s *stack) do(method, path string, body []byte, contentType string) (*http.Response, []byte) {
	s.t.Helper()
	req, err := http.NewRequest(method, s.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, raw
}

func (s *stack) json(method, path string, body any, out any, want int) []byte {
	s.t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	resp, raw := s.do(method, path, payload, "application/json")
	if resp.StatusCode != want {
		s.t.Fatalf("%s %s: status %d want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			s.t.Fatalf("%s %s: decode: %v: %s", method, path, err, raw)
		}
	}
	return raw
}

type fileView struct {
	meta.File
	Facets []string `json:"facets"`
	URLs   struct {
		Content, Download, Preview, Thumbnail string
	} `json:"urls"`
}

type searchResult struct {
	Total int `json:"total"`
	Hits  []struct {
		File    fileView `json:"file"`
		Page    int64    `json:"page"`
		Snippet string   `json:"snippet"`
		Fresh   bool     `json:"fresh"`
	} `json:"hits"`
	Overlay int `json:"overlay"`
}

type facetsResult struct {
	Children []struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Count int64  `json:"count"`
	} `json:"children"`
}

// upload runs the streamuploader flow and registers the file with the Drive.
func (s *stack) upload(name, contentType string, body []byte, tags ...string) fileView {
	s.t.Helper()
	var key model.CreateUploadKeyResponse
	s.json(http.MethodPost, "/api/upload/keys", map[string]any{"file_name": name, "content_type": contentType, "size_bytes": len(body)}, &key, http.StatusCreated)
	resp, raw := s.do(http.MethodPut, "/api/upload/keys/"+key.UploadKey+"/content", body, contentType)
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("upload %s: status %d: %s", name, resp.StatusCode, raw)
	}
	var item *model.UploadItem
	for i := 0; i < 30 && item == nil; i++ {
		var wait model.WaitUploadsResponse
		s.json(http.MethodPost, "/api/upload/wait", map[string]any{"upload_keys": []string{key.UploadKey}, "timeout_seconds": 2}, &wait, http.StatusOK)
		if len(wait.Items) == 1 && wait.Items[0].Status == model.UploadUploaded {
			item = wait.Items[0]
		} else if len(wait.Items) == 1 && wait.Items[0].Status == model.UploadFailed {
			s.t.Fatalf("upload %s failed: %s", name, wait.Items[0].Error)
		}
	}
	if item == nil {
		s.t.Fatalf("upload %s never became ready", name)
	}
	var f fileView
	s.json(http.MethodPost, "/api/drive/files", map[string]any{"upload": item, "tags": tags}, &f, http.StatusCreated)
	return f
}

func (s *stack) find(query string) searchResult {
	s.t.Helper()
	var res searchResult
	s.json(http.MethodGet, "/api/drive/search?"+query, nil, &res, http.StatusOK)
	return res
}

func (s *stack) fileIDs(res searchResult) []string {
	ids := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		ids = append(ids, h.File.FileID)
	}
	return ids
}

func (s *stack) runIndexer() indexer.Report {
	s.t.Helper()
	rep, err := s.ix.RunOnce(s.ctx)
	if err != nil {
		s.t.Fatalf("indexer run: %v", err)
	}
	return rep
}

func fixture(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestAllInOneMilestone(t *testing.T) {
	store := memstore.New()
	s := newStack(t, store, filepath.Join(t.TempDir(), "index"))

	// --- Check 1: a docx and a jpeg uploaded through the API appear in a tag folder.
	docx := s.upload("文書グリッド.docx", docxType, fixture(t, "../../internal/docpreview/testdata/sample.docx"), "inbox")
	photo := s.upload("photo.jpg", "image/jpeg", makeJPEG(t), "inbox", "photos/2026")
	pdf := s.upload("空港の案内.pdf", pdfType, fixture(t, "../testdata/kuko-3pages.pdf"), "inbox")
	if docx.URLs.Preview == "" || docx.URLs.Thumbnail == "" {
		t.Fatalf("docx registration lacks derived assets: %+v", docx.URLs)
	}
	if photo.URLs.Thumbnail == "" {
		t.Fatalf("jpeg registration lacks a thumbnail: %+v", photo.URLs)
	}

	// Before the indexer ran, the folder listing comes from the journal overlay.
	fresh := s.find("tag=inbox&sort=name")
	if fresh.Total != 3 || len(fresh.Hits) != 3 {
		t.Fatalf("overlay folder listing: total %d hits %d", fresh.Total, len(fresh.Hits))
	}
	for _, h := range fresh.Hits {
		if !h.Fresh {
			t.Fatalf("expected overlay results to be marked fresh: %+v", h.File.Name)
		}
	}

	rep := s.runIndexer()
	if rep.FilesIndexed != 3 || rep.PendingText != 0 {
		t.Fatalf("first index run: %+v", rep)
	}
	folder := s.find("tag=inbox&sort=name")
	if folder.Total != 3 || folder.Overlay != 0 {
		t.Fatalf("indexed folder listing: total %d overlay %d", folder.Total, folder.Overlay)
	}
	ids := s.fileIDs(folder)
	if !contains(ids, docx.FileID) || !contains(ids, photo.FileID) || !contains(ids, pdf.FileID) {
		t.Fatalf("folder missing files: %v", ids)
	}
	// photos/2026 is a nested tag folder: exact listing shows the photo only.
	nested := s.find("tag=photos/2026")
	if nested.Total != 1 || nested.Hits[0].File.FileID != photo.FileID {
		t.Fatalf("nested folder: %+v", nested)
	}
	var facets facetsResult
	s.json(http.MethodGet, "/api/drive/facets?path=/tags", nil, &facets, http.StatusOK)
	counts := map[string]int64{}
	for _, c := range facets.Children {
		counts[c.Path] = c.Count
	}
	if counts["/tags/inbox"] != 3 || counts["/tags/photos"] != 1 {
		t.Fatalf("facets at /tags: %+v", facets.Children)
	}

	// --- Check 2: a Japanese phrase from page 3 of the PDF returns that file with page 3.
	hit := s.find("q=" + urlQuery("大阪湾の人工島"))
	if len(hit.Hits) == 0 || hit.Hits[0].File.FileID != pdf.FileID {
		t.Fatalf("page search: %+v", hit)
	}
	if hit.Hits[0].Page != 3 || !strings.Contains(hit.Hits[0].Snippet, "<b>") {
		t.Fatalf("expected page 3 with a highlighted snippet, got page %d snippet %q", hit.Hits[0].Page, hit.Hits[0].Snippet)
	}
	// The docx body is searchable too; "項目 9" appears on page 3 of the docx.
	docxHit := s.find("q=" + urlQuery("項目 9"))
	if len(docxHit.Hits) == 0 || docxHit.Hits[0].File.FileID != docx.FileID || docxHit.Hits[0].Page != 3 {
		t.Fatalf("docx page search: %+v", docxHit)
	}
	// Author search: the PDF Info author and the docx Dublin Core creator.
	if res := s.find("q=" + urlQuery("国土")); len(res.Hits) == 0 || res.Hits[0].File.FileID != pdf.FileID {
		t.Fatalf("pdf author search: %+v", res)
	}
	if res := s.find("q=BDF"); len(res.Hits) == 0 || res.Hits[0].File.FileID != docx.FileID {
		t.Fatalf("docx author search: %+v", res)
	}
	var detail fileView
	s.json(http.MethodGet, "/api/drive/files/"+pdf.FileID, nil, &detail, http.StatusOK)
	if detail.Author.Extracted != "国土 交通" {
		t.Fatalf("extracted author = %q", detail.Author.Extracted)
	}
	if !contains(detail.Facets, "/author/国土 交通") || !contains(detail.Facets, "/type/pdf") {
		t.Fatalf("facets = %v", detail.Facets)
	}
	// Name search by partial match.
	if res := s.find("q=" + urlQuery("空港")); len(res.Hits) == 0 || res.Hits[0].File.FileID != pdf.FileID {
		t.Fatalf("name search: %+v", res)
	}

	// --- Check 4 (server side): the preview is served as BDF with range support; thumbnails are images.
	req, _ := http.NewRequest(http.MethodGet, s.srv.URL+pdf.URLs.Preview, nil)
	req.Header.Set("Range", "bytes=0-99")
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Type") != "application/x-bdf" || len(part) != 100 || !strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-99/") {
		t.Fatalf("preview range: status %d type %q len %d range %q", resp.StatusCode, resp.Header.Get("Content-Type"), len(part), resp.Header.Get("Content-Range"))
	}
	resp, raw := s.do(http.MethodGet, photo.URLs.Thumbnail, nil, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") || len(raw) == 0 {
		t.Fatalf("jpeg thumbnail: status %d type %q len %d", resp.StatusCode, resp.Header.Get("Content-Type"), len(raw))
	}
	resp, raw = s.do(http.MethodGet, photo.URLs.Content, nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(raw) == 0 {
		t.Fatalf("jpeg content: status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = s.do(http.MethodGet, pdf.URLs.Download, nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: status %d disposition %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	resp, raw = s.do(http.MethodGet, "/", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "/ui/app.js") {
		t.Fatalf("ui index: status %d", resp.StatusCode)
	}
	resp, _ = s.do(http.MethodGet, "/ui/app.js", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ui bundle: status %d", resp.StatusCode)
	}

	// --- Metadata edit: moving a file between tag folders is visible at once and survives indexing.
	var moved fileView
	s.json(http.MethodPatch, "/api/drive/files/"+docx.FileID, map[string]any{"tags": []string{"archive/2026"}, "expected_revision": docx.Revision}, &moved, http.StatusOK)
	if moved.Revision != docx.Revision+1 || len(moved.Tags) != 1 || moved.Tags[0] != "/archive/2026" {
		t.Fatalf("patched file: %+v", moved.File)
	}
	if res := s.find("tag=inbox"); contains(s.fileIDs(res), docx.FileID) || res.Total != 2 {
		t.Fatalf("overlay should hide the moved file from inbox: %v total %d", s.fileIDs(res), res.Total)
	}
	if res := s.find("tag=archive/2026"); !contains(s.fileIDs(res), docx.FileID) || res.Total != 1 {
		t.Fatalf("overlay should show the moved file in archive/2026: %v", s.fileIDs(res))
	}
	s.json(http.MethodGet, "/api/drive/facets?path=/tags", nil, &facets, http.StatusOK)
	counts = map[string]int64{}
	for _, c := range facets.Children {
		counts[c.Path] = c.Count
	}
	if counts["/tags/inbox"] != 2 || counts["/tags/archive"] != 1 {
		t.Fatalf("overlay-adjusted facets: %+v", facets.Children)
	}
	// A stale revision is refused.
	resp, _ = s.do(http.MethodPatch, "/api/drive/files/"+docx.FileID, []byte(`{"name":"x","expected_revision":1}`), "application/json")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale revision status %d", resp.StatusCode)
	}
	s.runIndexer()
	if res := s.find("tag=inbox"); res.Total != 2 || res.Overlay != 0 {
		t.Fatalf("indexed inbox after move: total %d overlay %d", res.Total, res.Overlay)
	}
	if res := s.find("tag=archive/2026"); res.Total != 1 || res.Hits[0].File.FileID != docx.FileID {
		t.Fatalf("indexed archive after move: %+v", res)
	}

	// --- Check 3: deleting the index directory and rebuilding restores identical results.
	before := s.find("q=" + urlQuery("大阪湾の人工島"))
	beforeFolder := s.fileIDs(s.find("tag=inbox&sort=name"))
	if _, err := s.ix.Rebuild(s.ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	after := s.find("q=" + urlQuery("大阪湾の人工島"))
	if len(after.Hits) != len(before.Hits) || after.Hits[0].File.FileID != before.Hits[0].File.FileID || after.Hits[0].Page != before.Hits[0].Page {
		t.Fatalf("rebuild changed results: before %+v after %+v", before.Hits, after.Hits)
	}
	afterFolder := s.fileIDs(s.find("tag=inbox&sort=name"))
	if strings.Join(afterFolder, ",") != strings.Join(beforeFolder, ",") {
		t.Fatalf("rebuild changed folder listing: %v vs %v", beforeFolder, afterFolder)
	}

	// A brand-new process with an empty index directory rebuilds from the bucket on start.
	s2 := newStack(t, store, filepath.Join(t.TempDir(), "index2"))
	if s2.metas.Len() != 3 {
		t.Fatalf("second stack loaded %d files, want 3", s2.metas.Len())
	}
	cold := s2.find("q=" + urlQuery("大阪湾の人工島"))
	if len(cold.Hits) != 1 || cold.Hits[0].File.FileID != pdf.FileID || cold.Hits[0].Page != 3 {
		t.Fatalf("cold start search: %+v", cold)
	}
	if res := s2.find("tag=archive/2026"); res.Total != 1 || res.Hits[0].File.FileID != docx.FileID {
		t.Fatalf("cold start folder: %+v", res)
	}

	// --- Delete: gone from listings at once and from the index after a run.
	resp, _ = s.do(http.MethodDelete, "/api/drive/files/"+photo.FileID, nil, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	if res := s.find("tag=inbox"); contains(s.fileIDs(res), photo.FileID) {
		t.Fatalf("deleted file still listed through overlay")
	}
	resp, _ = s.do(http.MethodGet, "/api/drive/files/"+photo.FileID, nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted file GET status %d", resp.StatusCode)
	}
	rep = s.runIndexer()
	if rep.FilesDeleted != 1 {
		t.Fatalf("delete run: %+v", rep)
	}
	if res := s.find("tag=inbox"); res.Total != 1 || res.Overlay != 0 {
		t.Fatalf("inbox after delete: total %d", res.Total)
	}
	if res := s.find("tag=photos/2026"); res.Total != 0 {
		t.Fatalf("photos folder after delete: total %d", res.Total)
	}
	var stats map[string]any
	s.json(http.MethodGet, "/api/drive/stats", nil, &stats, http.StatusOK)
	if stats["files_cached"].(float64) != 3 {
		t.Fatalf("stats: %+v", stats)
	}
}

func urlQuery(s string) string {
	return url.QueryEscape(s)
}

// refresh makes a server-only stack load the latest published generation.
func (s *stack) refresh() bool {
	s.t.Helper()
	changed, err := s.follower.Refresh(s.ctx)
	if err != nil {
		s.t.Fatalf("follower refresh: %v", err)
	}
	return changed
}

func (s *stack) stats() map[string]any {
	s.t.Helper()
	var out map[string]any
	s.json(http.MethodGet, "/api/drive/stats", nil, &out, http.StatusOK)
	return out
}

func generationOf(stats map[string]any) int64 {
	ix, _ := stats["indexer"].(map[string]any)
	g, _ := ix["generation"].(float64)
	return int64(g)
}

// TestServerIndexerSplit covers the m2 scenario: an indexer publishes the
// index to the bucket as a snapshot and separate server processes follow it
// with a read-only sidecar, while edits stay visible at once through the
// journal overlay. A restarted indexer with an empty directory adopts the
// snapshot instead of rebuilding.
func TestServerIndexerSplit(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	snapStore := &snapshot.Store{Objects: store, Bucket: "bucket", Prefix: "drive/", Tenant: "t1"}

	// A server started before any indexer ran serves an empty index and
	// still answers; the overlay shows registrations at once.
	early := newServerStack(t, store, filepath.Join(t.TempDir(), "early"))
	if res := early.find("tag=inbox"); res.Total != 0 {
		t.Fatalf("empty server: %+v", res)
	}
	if generationOf(early.stats()) != 0 {
		t.Fatalf("early server stats: %+v", early.stats())
	}

	// The all-in-one (or indexer) process: upload, register, fold, publish.
	a := newStack(t, store, filepath.Join(t.TempDir(), "a"))
	if a.startRep.Adopted || a.startRep.Rebuilt != true {
		t.Fatalf("first indexer start on an empty bucket: %+v", a.startRep)
	}
	docx := a.upload("文書グリッド.docx", docxType, fixture(t, "../../internal/docpreview/testdata/sample.docx"), "inbox")
	pdf := a.upload("空港の案内.pdf", pdfType, fixture(t, "../testdata/kuko-3pages.pdf"), "inbox")
	if res := early.find("tag=inbox&sort=name"); res.Total != 2 || !res.Hits[0].Fresh {
		t.Fatalf("overlay on the early server: %+v", res)
	}
	rep := a.runIndexer()
	if rep.FilesIndexed != 2 || !rep.Published || rep.Generation < 1 {
		t.Fatalf("first fold should publish: %+v", rep)
	}
	p, _, found, err := snapStore.ReadPointer(ctx)
	if err != nil || !found || p.Generation != rep.Generation || p.NumDocs == 0 || len(p.Files) == 0 || p.LastJournalKey == "" {
		t.Fatalf("published pointer: %+v found %v err %v", p, found, err)
	}
	for _, f := range p.Files {
		if _, err := store.HeadObject(ctx, storage.HeadInput{Bucket: "bucket", Key: snapStore.FileKey(f.Name)}); err != nil {
			t.Fatalf("segment %s not in the bucket: %v", f.Name, err)
		}
	}

	// A server-only process started now loads the snapshot from the bucket.
	b := newServerStack(t, store, filepath.Join(t.TempDir(), "b"))
	if b.metas.Len() != 2 || generationOf(b.stats()) != p.Generation {
		t.Fatalf("server b after start: files %d stats %+v", b.metas.Len(), b.stats())
	}
	hit := b.find("q=" + urlQuery("大阪湾の人工島"))
	if len(hit.Hits) != 1 || hit.Hits[0].File.FileID != pdf.FileID || hit.Hits[0].Page != 3 || hit.Hits[0].Fresh {
		t.Fatalf("page search on the server: %+v", hit)
	}
	folder := b.find("tag=inbox&sort=name")
	if folder.Total != 2 || folder.Overlay != 0 {
		t.Fatalf("folder on the server: total %d overlay %d", folder.Total, folder.Overlay)
	}
	var facets facetsResult
	b.json(http.MethodGet, "/api/drive/facets?path=/tags", nil, &facets, http.StatusOK)
	if len(facets.Children) != 1 || facets.Children[0].Path != "/tags/inbox" || facets.Children[0].Count != 2 {
		t.Fatalf("facets on the server: %+v", facets.Children)
	}
	// The early server catches up on its next refresh.
	if !early.refresh() || early.metas.Len() != 2 {
		t.Fatalf("early server did not pick up generation %d", p.Generation)
	}
	if res := early.find("tag=inbox"); res.Total != 2 || res.Overlay != 0 {
		t.Fatalf("early server after refresh: %+v", res)
	}
	// Rebuilding is the indexer's job.
	if resp, _ := b.do(http.MethodPost, "/api/drive/admin/reindex", nil, ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("reindex on a server: status %d", resp.StatusCode)
	}

	// An edit through the all-in-one process is visible on the server at
	// once through the overlay and from the index after the next generation.
	var moved fileView
	a.json(http.MethodPatch, "/api/drive/files/"+docx.FileID, map[string]any{"tags": []string{"archive/2026"}, "expected_revision": docx.Revision}, &moved, http.StatusOK)
	if res := b.find("tag=archive/2026"); res.Total != 1 || res.Hits[0].File.FileID != docx.FileID || !res.Hits[0].Fresh {
		t.Fatalf("overlay edit on the server: %+v", res)
	}
	if res := b.find("tag=inbox"); res.Total != 1 {
		t.Fatalf("overlay should hide the moved file: %+v", res)
	}
	rep = a.runIndexer()
	if !rep.Published || rep.Generation != p.Generation+1 {
		t.Fatalf("second fold: %+v", rep)
	}
	if !b.refresh() {
		t.Fatal("server did not see the new generation")
	}
	if b.refresh() {
		t.Fatal("a second refresh without a new generation should be a no-op")
	}
	if res := b.find("tag=archive/2026"); res.Total != 1 || res.Overlay != 0 || res.Hits[0].Fresh {
		t.Fatalf("indexed edit on the server: %+v", res)
	}
	if generationOf(b.stats()) != rep.Generation {
		t.Fatalf("server generation: %+v", b.stats())
	}
	var detail fileView
	b.json(http.MethodGet, "/api/drive/files/"+docx.FileID, nil, &detail, http.StatusOK)
	if len(detail.Tags) != 1 || detail.Tags[0] != "/archive/2026" || detail.Author.Extracted == "" {
		t.Fatalf("server meta cache after incremental refresh: %+v", detail.File)
	}

	// A delete reaches the server the same way.
	if resp, _ := a.do(http.MethodDelete, "/api/drive/files/"+pdf.FileID, nil, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	if res := b.find("q=" + urlQuery("大阪湾の人工島")); len(res.Hits) != 0 {
		t.Fatalf("deleted file still found through the overlay: %+v", res)
	}
	a.runIndexer()
	b.refresh()
	if res := b.find("q=" + urlQuery("大阪湾の人工島")); len(res.Hits) != 0 || res.Overlay != 0 {
		t.Fatalf("deleted file still indexed on the server: %+v", res)
	}
	if resp, _ := b.do(http.MethodGet, "/api/drive/files/"+pdf.FileID, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted file GET on the server: %d", resp.StatusCode)
	}

	// A new indexer process with an empty directory adopts the published
	// snapshot instead of rebuilding, and continues from its journal key.
	a2 := newStack(t, store, filepath.Join(t.TempDir(), "a2"))
	if !a2.startRep.Adopted || a2.startRep.Rebuilt || a2.startRep.Published {
		t.Fatalf("restarted indexer should adopt the snapshot: %+v", a2.startRep)
	}
	if res := a2.find("tag=archive/2026"); res.Total != 1 || res.Hits[0].File.FileID != docx.FileID || res.Overlay != 0 {
		t.Fatalf("adopted index: %+v", res)
	}
	if a2.ix.LastJournalKey() != a.ix.LastJournalKey() || a2.ix.Generation() != a.ix.Generation() {
		t.Fatalf("adopted state: key %q vs %q, generation %d vs %d", a2.ix.LastJournalKey(), a.ix.LastJournalKey(), a2.ix.Generation(), a.ix.Generation())
	}
	// Local segment files equal the published list; nothing else lingers.
	p, _, _, _ = snapStore.ReadPointer(ctx)
	want := map[string]bool{}
	for _, f := range p.Files {
		want[f.Name] = true
	}
	entries, _ := os.ReadDir(a2.index)
	for _, e := range entries {
		if snapshot.IsSegmentFile(e.Name()) && !want[e.Name()] {
			t.Fatalf("adopted directory holds unreferenced segment file %s", e.Name())
		}
		delete(want, e.Name())
	}
	if len(want) != 0 {
		t.Fatalf("adopted directory lacks %v", want)
	}
}
