package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"streamuploader/drive/audit"
	"streamuploader/drive/indexer"
	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	driveserver "streamuploader/drive/server"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/worm"
	"streamuploader/internal/config"
	"streamuploader/internal/model"
	suserver "streamuploader/internal/server"
	"streamuploader/internal/storage"
)

// newWORMStack is newStack with WORM mode, a legal-hold object lock on the
// locked key classes and audit checkpoints, the way a WORM deployment runs.
func newWORMStack(t *testing.T, store *memstore.Store, indexDir string, mode worm.Mode, window time.Duration) *stack {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	security := config.DefaultSecurityPolicy()
	lock := storage.LockPolicy{Mode: storage.LockModeLegalHold}
	cfg := config.Config{
		Mode:           "standalone_cross_origin",
		PublicBaseURL:  "http://example.test",
		UploadBasePath: "/api/upload",
		AllowedOrigins: []string{"*"},
		Bucket:         "bucket",
		SessionTTL:     time.Hour,
		MaxUploadBytes: 8 << 20,
		Security:       security,
		ObjectLock:     lock,
		WORMMode:       true,
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
	jstore := &journal.Store{Objects: store, Bucket: cfg.Bucket, Prefix: "drive/", Lock: lock}
	metas := meta.NewCache()
	ix := indexer.New(indexer.Config{Tenant: "t1", Bucket: cfg.Bucket, Prefix: "drive/", IndexDir: indexDir, Checkpoints: true, Lock: lock}, store, jstore, search, metas, nil)
	if _, err := ix.Start(ctx); err != nil {
		t.Fatalf("indexer start: %v", err)
	}
	uploader := suserver.New(cfg, store).Handler()
	drv := driveserver.New(driveserver.Config{Tenant: "t1", Bucket: cfg.Bucket, Prefix: "drive/", OverlayTTL: time.Millisecond, WORM: mode, AccessWindow: window, ObjectLock: lock}, driveserver.Deps{
		Store: store, Journal: jstore, Metas: metas, Search: search, Indexer: ix, Uploader: uploader, Chain: ix.Chain(),
	})
	srv := httptest.NewServer(drv.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &stack{t: t, ctx: ctx, store: store, cfg: cfg, search: search, metas: metas, ix: ix, srv: srv, client: &http.Client{Jar: jar}, index: indexDir}
}

// journalEvents reads every event of the tenant, oldest first.
func (s *stack) journalEvents() []journal.Entry {
	s.t.Helper()
	js := &journal.Store{Objects: s.store, Bucket: s.cfg.Bucket, Prefix: "drive/"}
	entries, err := js.ListRange(s.ctx, "t1", "", "")
	if err != nil {
		s.t.Fatal(err)
	}
	return entries
}

func (s *stack) accessEvents() []journal.Entry {
	var out []journal.Entry
	for _, e := range s.journalEvents() {
		if e.Event.Type == journal.FileAccessed {
			out = append(out, e)
		}
	}
	return out
}

func (s *stack) expectReadonly(method, path string, body []byte) {
	s.t.Helper()
	resp, raw := s.do(method, path, body, "application/json")
	var apiErr struct{ Code string }
	_ = json.Unmarshal(raw, &apiErr)
	if resp.StatusCode != http.StatusMethodNotAllowed || apiErr.Code != "worm_readonly" {
		s.t.Fatalf("%s %s: status %d code %q body %s", method, path, resp.StatusCode, apiErr.Code, raw)
	}
}

func TestWORMStrict(t *testing.T) {
	store := memstore.New()
	s := newWORMStack(t, store, filepath.Join(t.TempDir(), "index"), worm.Strict, time.Minute)

	var info struct {
		WORM struct {
			Mode        string `json:"mode"`
			Enabled     bool   `json:"enabled"`
			Enforcement string `json:"enforcement"`
			ReadLogging string `json:"read_logging"`
		} `json:"worm"`
	}
	s.json(http.MethodGet, "/api/drive/info", nil, &info, http.StatusOK)
	if info.WORM.Mode != "strict" || !info.WORM.Enabled || info.WORM.ReadLogging != "proxy" || !strings.Contains(info.WORM.Enforcement, "storage lock") {
		t.Fatalf("info: %+v", info.WORM)
	}

	pdf := s.upload("空港の案内.pdf", pdfType, fixture(t, "../testdata/kuko-3pages.pdf"), "audit")
	photo := s.upload("photo.jpg", "image/jpeg", makeJPEG(t), "audit")

	// --- Storage layer: the original and the journal carry the lock and refuse a delete.
	if r := store.Retention("bucket", pdf.ObjectKey); r == nil || !r.LegalHold {
		t.Fatalf("original is not locked: %+v", r)
	}
	if err := store.DeleteObject(s.ctx, storage.DeleteInput{Bucket: "bucket", Key: pdf.ObjectKey}); err == nil {
		t.Fatal("storage lock did not refuse deleting the original")
	}
	if r := store.Retention("bucket", pdf.Derived.Thumbnail.ObjectKey); r != nil {
		t.Fatalf("derived asset must not be locked: %+v", r)
	}
	events := s.journalEvents()
	if len(events) != 2 {
		t.Fatalf("journal has %d events, want 2", len(events))
	}
	if err := store.DeleteObject(s.ctx, storage.DeleteInput{Bucket: "bucket", Key: events[0].Key}); err == nil {
		t.Fatal("storage lock did not refuse deleting a journal event")
	}

	// --- Application layer: no edit, no delete, no restore; 405 worm_readonly.
	s.expectReadonly(http.MethodPatch, "/api/drive/files/"+pdf.FileID, []byte(`{"tags":["audit","more"]}`))
	s.expectReadonly(http.MethodDelete, "/api/drive/files/"+pdf.FileID, nil)
	s.expectReadonly(http.MethodPost, "/api/drive/files/"+pdf.FileID+"/restore", nil)
	var still fileView
	s.json(http.MethodGet, "/api/drive/files/"+pdf.FileID, nil, &still, http.StatusOK)
	if still.Revision != 1 || len(still.Tags) != 1 {
		t.Fatalf("file changed despite refusals: %+v", still.File)
	}

	// --- Access logging: every read kind produces a file.accessed event; a
	// viewer's repeated range requests within the window do not.
	resp, _ := s.do(http.MethodGet, pdf.URLs.Download, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status %d", resp.StatusCode)
	}
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, s.srv.URL+pdf.URLs.Preview, nil)
		req.Header.Set("Range", "bytes=0-99")
		r, err := s.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusPartialContent {
			t.Fatalf("preview range status %d", r.StatusCode)
		}
	}
	resp, _ = s.do(http.MethodGet, photo.URLs.Thumbnail, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumbnail status %d", resp.StatusCode)
	}
	resp, _ = s.do(http.MethodGet, photo.URLs.Content, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("content status %d", resp.StatusCode)
	}
	accesses := s.accessEvents()
	kinds := map[string]journal.Entry{}
	for _, e := range accesses {
		kinds[e.Event.Access.Kind+":"+e.Event.FileID] = e
	}
	if len(accesses) != 4 {
		t.Fatalf("got %d access events, want 4 (download, preview, thumbnail, inline): %v", len(accesses), kinds)
	}
	dl := kinds["download:"+pdf.FileID]
	if dl.Event == nil || dl.Event.Access.ObjectKey != pdf.ObjectKey || dl.Event.Actor == "" || dl.Event.At.IsZero() || dl.Event.Access.ClientHash == "" {
		t.Fatalf("download access event incomplete: %+v", dl.Event)
	}
	if _, ok := kinds["preview:"+pdf.FileID]; !ok {
		t.Fatalf("preview access missing: %v", kinds)
	}
	if kinds["inline:"+photo.FileID].Event.Access.ObjectKey != photo.ObjectKey {
		t.Fatalf("inline access event: %+v", kinds["inline:"+photo.FileID].Event)
	}
	// Access events are locked like every other journal object.
	if r := store.Retention("bucket", dl.Key); r == nil || !r.LegalHold {
		t.Fatalf("access event not locked: %+v", r)
	}

	// --- Versions: the only edit path. The old object stays and is readable.
	v2 := s.uploadOnly("空港の案内-v2.pdf", pdfType, fixture(t, "../testdata/kuko-3pages.pdf"))
	var versioned fileView
	s.json(http.MethodPost, "/api/drive/files/"+pdf.FileID+"/versions", map[string]any{"upload": v2, "expected_revision": 1}, &versioned, http.StatusCreated)
	if versioned.Revision != 2 || versioned.ObjectKey != v2.ObjectKey || len(versioned.Versions) != 1 || versioned.Versions[0].ObjectKey != pdf.ObjectKey || versioned.Name != pdf.Name {
		t.Fatalf("versioned file: %+v", versioned.File)
	}
	var versions struct {
		Versions []struct {
			N    int                                `json:"n"`
			URLs struct{ Content, Download string } `json:"urls"`
		} `json:"versions"`
	}
	s.json(http.MethodGet, "/api/drive/files/"+pdf.FileID+"/versions", nil, &versions, http.StatusOK)
	if len(versions.Versions) != 1 || versions.Versions[0].N != 1 {
		t.Fatalf("versions listing: %+v", versions)
	}
	resp, raw := s.do(http.MethodGet, versions.Versions[0].URLs.Download, nil, "")
	if resp.StatusCode != http.StatusOK || len(raw) == 0 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("old version download: status %d len %d", resp.StatusCode, len(raw))
	}
	if _, err := store.HeadObject(s.ctx, storage.HeadInput{Bucket: "bucket", Key: pdf.ObjectKey}); err != nil {
		t.Fatalf("old original gone: %v", err)
	}
	// Re-using an object as a version is refused; a stale revision too.
	resp, _ = s.do(http.MethodPost, "/api/drive/files/"+pdf.FileID+"/versions", mustJSON(map[string]any{"upload": v2}), "application/json")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate version status %d", resp.StatusCode)
	}

	// --- Tamper evidence: the fold writes a checkpoint chain that verifies.
	rep := s.runIndexer()
	if rep.Checkpoint != 1 {
		t.Fatalf("first fold did not write checkpoint 1: %+v", rep)
	}
	chain := s.ix.Chain()
	if chain == nil {
		t.Fatal("indexer has no chain")
	}
	js := &journal.Store{Objects: store, Bucket: "bucket", Prefix: "drive/"}
	verified, err := chain.Verify(s.ctx, js, audit.VerifyOptions{HashOriginals: true})
	if err != nil {
		t.Fatal(err)
	}
	all := s.journalEvents()
	if !verified.OK() || verified.Checkpoints != 1 || verified.EventsVerified != len(all) || verified.OriginalsVerified != 3 || verified.UnverifiedTail != 0 {
		t.Fatalf("verify after fold: %+v (journal %d)", verified, len(all))
	}
	var cps struct {
		Total       int `json:"total"`
		Checkpoints []struct {
			N          int64 `json:"n"`
			EventCount int   `json:"event_count"`
			Originals  int   `json:"originals"`
		} `json:"checkpoints"`
	}
	s.json(http.MethodGet, "/api/drive/checkpoints", nil, &cps, http.StatusOK)
	if cps.Total != 1 || cps.Checkpoints[0].N != 1 || cps.Checkpoints[0].EventCount != len(all) || cps.Checkpoints[0].Originals != 3 {
		t.Fatalf("checkpoints API: %+v", cps)
	}
	var cp audit.Checkpoint
	s.json(http.MethodGet, "/api/drive/checkpoints/1", nil, &cp, http.StatusOK)
	if cp.PrevCheckpointSHA256 != audit.Genesis("t1") || audit.MerkleRoot(cp.Leaves) != cp.JournalMerkleRoot {
		t.Fatalf("checkpoint 1 body: %+v", cp)
	}
	// Another read, another fold, a second link; nothing is chained twice.
	resp, _ = s.do(http.MethodGet, photo.URLs.Download, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second download status %d", resp.StatusCode)
	}
	if rep = s.runIndexer(); rep.Checkpoint != 2 {
		t.Fatalf("second fold: %+v", rep)
	}
	if rep = s.runIndexer(); rep.Checkpoint != 0 {
		t.Fatalf("idle fold wrote a checkpoint: %+v", rep)
	}
	verified, _ = chain.Verify(s.ctx, js, audit.VerifyOptions{})
	if !verified.OK() || verified.Checkpoints != 2 || verified.EventsVerified != len(s.journalEvents()) {
		t.Fatalf("verify after second fold: %+v", verified)
	}

	// Journal export streams JSON lines, filterable by type.
	resp, raw = s.do(http.MethodGet, "/api/drive/journal?type=file.accessed", nil, "")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if resp.StatusCode != http.StatusOK || len(lines) != 5 || !strings.Contains(lines[0], `"file.accessed"`) {
		t.Fatalf("journal export: status %d lines %d: %s", resp.StatusCode, len(lines), raw)
	}

	// Removing one journal object out of band (bucket administrator) makes
	// verification fail and name the event.
	victim := all[1].Key
	store.Remove("bucket", victim)
	verified, _ = chain.Verify(s.ctx, js, audit.VerifyOptions{})
	if verified.OK() || len(verified.Findings) != 1 || verified.Findings[0].Kind != audit.FindingMissingEvent || verified.Findings[0].Key != victim || verified.Findings[0].Checkpoint != 1 {
		t.Fatalf("tamper not detected: %+v", verified.Findings)
	}

	// A fresh process continues the chain from the bucket instead of
	// starting over: the head is checkpoint 2, and a new fold writes 3.
	s2 := newWORMStack(t, store, filepath.Join(t.TempDir(), "index2"), worm.Strict, time.Minute)
	resp, _ = s2.do(http.MethodGet, photo.URLs.Download, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download on second stack: %d", resp.StatusCode)
	}
	if rep = s2.runIndexer(); rep.Checkpoint != 3 {
		t.Fatalf("second process fold: %+v", rep)
	}
}

func TestWORMAppendOnly(t *testing.T) {
	store := memstore.New()
	s := newWORMStack(t, store, filepath.Join(t.TempDir(), "index"), worm.AppendOnly, 0)
	photo := s.upload("photo.jpg", "image/jpeg", makeJPEG(t), "inbox")

	// Adding a tag, a first author and a first location are additions.
	var added fileView
	s.json(http.MethodPatch, "/api/drive/files/"+photo.FileID, map[string]any{"tags": []string{"inbox", "projects/x"}, "author": "me", "location": map[string]float64{"lat": 35.0, "lon": 139.0}}, &added, http.StatusOK)
	if len(added.Tags) != 2 || added.Author.Override != "me" || added.Location == nil {
		t.Fatalf("additive patch: %+v", added.File)
	}
	// Removing a tag, renaming, rewriting the author or clearing the location are refused.
	s.expectReadonly(http.MethodPatch, "/api/drive/files/"+photo.FileID, []byte(`{"tags":["projects/x"]}`))
	s.expectReadonly(http.MethodPatch, "/api/drive/files/"+photo.FileID, []byte(`{"name":"renamed.jpg"}`))
	s.expectReadonly(http.MethodPatch, "/api/drive/files/"+photo.FileID, []byte(`{"author":"you"}`))
	s.expectReadonly(http.MethodPatch, "/api/drive/files/"+photo.FileID, []byte(`{"clear_location":true}`))
	s.expectReadonly(http.MethodDelete, "/api/drive/files/"+photo.FileID, nil)
	var cur fileView
	s.json(http.MethodGet, "/api/drive/files/"+photo.FileID, nil, &cur, http.StatusOK)
	if cur.Revision != 2 || len(cur.Tags) != 2 || cur.Author.Override != "me" {
		t.Fatalf("state after refusals: %+v", cur.File)
	}
	// With a zero window every read is an event.
	for i := 0; i < 2; i++ {
		resp, _ := s.do(http.MethodGet, photo.URLs.Content, nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("content status %d", resp.StatusCode)
		}
	}
	if n := len(s.accessEvents()); n != 2 {
		t.Fatalf("access events = %d, want 2", n)
	}
	// HEAD is not a read.
	req, _ := http.NewRequest(http.MethodHead, s.srv.URL+photo.URLs.Content, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if n := len(s.accessEvents()); n != 2 {
		t.Fatalf("HEAD produced an access event: %d", n)
	}
	if rep := s.runIndexer(); rep.Checkpoint != 1 {
		t.Fatalf("fold: %+v", rep)
	}
}

// uploadOnly runs the streamuploader flow without registering the file.
func (s *stack) uploadOnly(name, contentType string, body []byte) *model.UploadItem {
	s.t.Helper()
	var key model.CreateUploadKeyResponse
	s.json(http.MethodPost, "/api/upload/keys", map[string]any{"file_name": name, "content_type": contentType, "size_bytes": len(body)}, &key, http.StatusCreated)
	resp, raw := s.do(http.MethodPut, "/api/upload/keys/"+key.UploadKey+"/content", body, contentType)
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("upload %s: status %d: %s", name, resp.StatusCode, raw)
	}
	for i := 0; i < 30; i++ {
		var wait model.WaitUploadsResponse
		s.json(http.MethodPost, "/api/upload/wait", map[string]any{"upload_keys": []string{key.UploadKey}, "timeout_seconds": 2}, &wait, http.StatusOK)
		if len(wait.Items) == 1 && wait.Items[0].Status == model.UploadUploaded {
			return wait.Items[0]
		}
		if len(wait.Items) == 1 && wait.Items[0].Status == model.UploadFailed {
			s.t.Fatalf("upload %s failed: %s", name, wait.Items[0].Error)
		}
	}
	s.t.Fatalf("upload %s never became ready", name)
	return nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
