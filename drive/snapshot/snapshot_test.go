package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"streamuploader/drive/memstore"
	"streamuploader/internal/storage"
)

const (
	segA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.idx"
	segB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.store"
	segC = "cccccccccccccccccccccccccccccccc.term"
	segD = "dddddddddddddddddddddddddddddddd.fast"
)

func writeSeg(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func metaJSON(opstamp int) string {
	return `{"index_settings":{},"segments":[],"schema":[],"opstamp":` + itoa(opstamp) + `}`
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newStore(t *testing.T, objects *memstore.Store, clk *clock) *Store {
	t.Helper()
	return &Store{Objects: objects, Bucket: "b", Prefix: "drive/", Tenant: "t1", Now: clk.Now}
}

func exists(t *testing.T, objects *memstore.Store, key string) bool {
	t.Helper()
	_, err := objects.HeadObject(context.Background(), storage.HeadInput{Bucket: "b", Key: key})
	if err == nil {
		return true
	}
	if errors.Is(err, memstore.ErrNotFound) {
		return false
	}
	t.Fatal(err)
	return false
}

func names(files []FileRef) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

func TestPointerRoundTrip(t *testing.T) {
	p := &Pointer{
		SchemaVersion: "1", Generation: 3, Opstamp: 42, LastJournalKey: "drive/journal/t1/2026/10/x.json",
		PendingText: map[string]string{"f1": "k1"}, Files: []FileRef{{Name: segA, Size: 10}},
		History: []Change{{Generation: 3, Changed: []string{"f1"}}},
		Meta:    []byte(`{"opstamp":42,"segments":[{"segment_id":"aaaa"}]}`),
	}
	body, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["opstamp"].(float64) != 42 || obj["drive"] == nil {
		t.Fatalf("encoded pointer: %s", body)
	}
	back, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if back.Generation != 3 || back.Opstamp != 42 || back.LastJournalKey != p.LastJournalKey || back.PendingText["f1"] != "k1" || len(back.Files) != 1 {
		t.Fatalf("decoded pointer: %+v", back)
	}
	var tantivy map[string]any
	if err := json.Unmarshal(back.Meta, &tantivy); err != nil {
		t.Fatal(err)
	}
	if _, has := tantivy["drive"]; has || tantivy["opstamp"].(float64) != 42 {
		t.Fatalf("tantivy meta should not carry the drive section: %s", back.Meta)
	}
	if _, err := Decode([]byte(`{"opstamp":1}`)); err == nil {
		t.Fatal("a pointer without a drive section must be rejected")
	}
	if _, err := Decode([]byte(`{"drive":{"files":[{"name":"../etc/passwd","size":1}]}}`)); err == nil {
		t.Fatal("a pointer naming a non-segment file must be rejected")
	}
}

func TestChangesSince(t *testing.T) {
	p := &Pointer{Generation: 5, History: []Change{
		{Generation: 3, Changed: []string{"a"}},
		{Generation: 4, Changed: []string{"b", "a"}},
		{Generation: 5, Changed: []string{"c"}},
	}}
	ids, full, ok := p.ChangesSince(5)
	if !ok || full || len(ids) != 0 {
		t.Fatalf("same generation: %v %v %v", ids, full, ok)
	}
	ids, full, ok = p.ChangesSince(3)
	if !ok || full || strings.Join(ids, ",") != "a,b,c" {
		t.Fatalf("from 3: %v %v %v", ids, full, ok)
	}
	if _, _, ok = p.ChangesSince(1); ok {
		t.Fatal("history does not reach generation 1")
	}
	if _, full, ok = p.ChangesSince(9); !ok || !full {
		t.Fatal("a pointer behind the follower means a full reload")
	}
	p.History[1].Full = true
	if _, full, ok = p.ChangesSince(2); !ok || !full {
		t.Fatal("a full change in range means a full reload")
	}
	h := appendHistory(nil, Change{Generation: 1, Changed: []string{"x"}})
	for g := int64(2); g <= 100; g++ {
		h = appendHistory(h, Change{Generation: g, Changed: []string{"x"}})
	}
	if len(h) != maxHistoryGenerations || h[0].Generation != 100-maxHistoryGenerations+1 {
		t.Fatalf("history not trimmed: %d entries, first %d", len(h), h[0].Generation)
	}
	h = appendHistory(h, Change{Generation: 101, Full: true})
	if len(h) != 1 || !h[0].Full {
		t.Fatalf("a full change should reset the history: %+v", h)
	}
}

func TestPublishDownloadRetireAndSweep(t *testing.T) {
	ctx := context.Background()
	objects := memstore.New()
	clk := &clock{now: time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)}
	s := newStore(t, objects, clk)
	src := t.TempDir()
	writeSeg(t, src, segA, "segment a")
	writeSeg(t, src, segB, "segment b")
	grace := 10 * time.Minute

	// Generation 1 uploads both files and the pointer.
	p1, etag1, err := s.Publish(ctx, Publication{MetaJSON: metaJSON(1), Files: []string{segA, segB}, Opstamp: 1, NumDocs: 2, SchemaVersion: "1", LastJournalKey: "k1", Changed: []string{"f1", "f1", "f2"}}, nil, src, grace)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Generation != 1 || etag1 == "" || len(p1.Files) != 2 || p1.Files[0].Size != 9 {
		t.Fatalf("generation 1: %+v etag %q", p1, etag1)
	}
	if strings.Join(p1.History[0].Changed, ",") != "f1,f2" {
		t.Fatalf("changed ids should be deduplicated: %v", p1.History[0].Changed)
	}
	for _, name := range []string{segA, segB, PointerName} {
		if !exists(t, objects, s.FileKey(name)) {
			t.Fatalf("%s not uploaded", name)
		}
	}
	got, etag, found, err := s.ReadPointer(ctx)
	if err != nil || !found || etag != etag1 || got.Generation != 1 || got.LastJournalKey != "k1" {
		t.Fatalf("read pointer: %+v %q %v %v", got, etag, found, err)
	}

	// A follower downloads into an empty directory, then nothing more.
	dst := t.TempDir()
	n, err := s.Download(ctx, got, dst)
	if err != nil || n != 2 {
		t.Fatalf("download: %d %v", n, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dst, segA)); string(body) != "segment a" {
		t.Fatalf("downloaded content %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(dst, "meta.json")); !strings.Contains(string(body), `"opstamp":1`) || strings.Contains(string(body), "drive") {
		t.Fatalf("local meta.json %q", body)
	}
	if n, err = s.Download(ctx, got, dst); err != nil || n != 0 {
		t.Fatalf("second download fetched %d: %v", n, err)
	}

	// Generation 2 drops A and adds C: A is retired, not deleted; only C is uploaded.
	writeSeg(t, src, segC, "segment c")
	clk.now = clk.now.Add(time.Minute)
	p2, _, err := s.Publish(ctx, Publication{MetaJSON: metaJSON(2), Files: []string{segB, segC}, Opstamp: 2, SchemaVersion: "1", LastJournalKey: "k2", Changed: []string{"f3"}}, p1, src, grace)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Generation != 2 || strings.Join(names(p2.Files), ",") != segB+","+segC || len(p2.Retired) != 1 || p2.Retired[0].Name != segA {
		t.Fatalf("generation 2: %+v", p2)
	}
	if !exists(t, objects, s.FileKey(segA)) {
		t.Fatal("retired file deleted before the grace period")
	}
	if ids, full, ok := p2.ChangesSince(1); !ok || full || strings.Join(ids, ",") != "f3" {
		t.Fatalf("changes since 1: %v %v %v", ids, full, ok)
	}

	// A crashed publish left an orphan; it is swept only once it is old.
	if _, err := objects.PutObject(ctx, storage.PutInput{Bucket: "b", Key: s.FileKey(segD), Body: strings.NewReader("orphan")}); err != nil {
		t.Fatal(err)
	}
	// The orphan's LastModified is the memstore's real clock.
	s.Now = time.Now
	if n, err := s.SweepOrphans(ctx, p2, grace); err != nil || n != 0 {
		t.Fatalf("young orphan swept: %d %v", n, err)
	}
	s.Now = func() time.Time { return time.Now().Add(2 * grace) }
	if n, err := s.SweepOrphans(ctx, p2, grace); err != nil || n != 1 {
		t.Fatalf("old orphan not swept: %d %v", n, err)
	}
	if exists(t, objects, s.FileKey(segD)) {
		t.Fatal("orphan still there")
	}
	s.Now = clk.Now

	// After the grace period the next publish deletes the retired file.
	clk.now = clk.now.Add(grace)
	p3, _, err := s.Publish(ctx, Publication{MetaJSON: metaJSON(3), Files: []string{segB, segC}, Opstamp: 3, SchemaVersion: "1", LastJournalKey: "k3"}, p2, src, grace)
	if err != nil {
		t.Fatal(err)
	}
	if len(p3.Retired) != 0 || exists(t, objects, s.FileKey(segA)) {
		t.Fatalf("retired file should be deleted after the grace period: %+v", p3.Retired)
	}

	// The follower moves from generation 1 to 3: C fetched, A pruned locally.
	if n, err := s.Download(ctx, p3, dst); err != nil || n != 1 {
		t.Fatalf("download delta: %d %v", n, err)
	}
	if n, err := Prune(dst, p3.Files); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	entries, _ := os.ReadDir(dst)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != segB+","+segC+",meta.json" {
		t.Fatalf("local dir after prune: %v", left)
	}

	// A file the sidecar listed but that is gone is reported, not skipped.
	_, _, err = s.Publish(ctx, Publication{MetaJSON: metaJSON(4), Files: []string{segB, segD}, SchemaVersion: "1"}, p3, src, grace)
	if !errors.Is(err, ErrLocalFileMissing) {
		t.Fatalf("missing local file: %v", err)
	}
	if _, _, err = s.Publish(ctx, Publication{MetaJSON: metaJSON(4), Files: []string{"../meta.json"}, SchemaVersion: "1"}, p3, src, grace); err == nil {
		t.Fatal("non-segment names must be refused")
	}
}
