package sidecar

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"streamuploader/drive/meta"
	"streamuploader/internal/extraction"
)

// testBinary finds the built sidecar or skips the test.
func testBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("DRIVE_SEARCH_BIN"); p != "" {
		return p
	}
	candidates := []string{
		filepath.Join("..", "..", "search", "target", "release", "drivesearch"),
		filepath.Join("..", "..", "search", "target", "debug", "drivesearch"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	t.Skip("drivesearch binary not built; run `cargo build --release` in search/ or set DRIVE_SEARCH_BIN")
	return ""
}

func TestGeohash(t *testing.T) {
	// Tokyo Station; the reference value is the well-known geohash.
	if got := Geohash(35.681236, 139.767125, 8); got != "xn76urx6" {
		t.Fatalf("geohash %q", got)
	}
}

func TestFacetPaths(t *testing.T) {
	shot := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	f := &meta.File{
		Name:     "成田/空港.PDF",
		Tags:     []string{"/projects/2026/alpha", "/photos"},
		Author:   meta.Author{Extracted: "A", Override: " B/C "},
		Dates:    meta.Dates{Shot: &shot},
		Location: &meta.Location{Lat: 35.681236, Lon: 139.767125},
	}
	paths := FacetPaths(f)
	want := map[string]bool{
		"/tags/projects/2026/alpha": true,
		"/tags/photos":              true,
		"/type/pdf":                 true,
		"/date/2026/10/08":          true,
		"/author/B／C":               true,
		"/geo/x/n/7/6/u/r/x/6":      true,
	}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected facet %q in %v", p, paths)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing facets %v", want)
	}
}

func TestSidecarJapaneseSearchAndFacets(t *testing.T) {
	bin := testBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := New(bin, filepath.Join(t.TempDir(), "index"))
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	up := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	narita := &meta.File{TenantID: "t1", FileID: "01A", Name: "成田国際空港の案内.pdf", SizeBytes: 300,
		Tags: []string{"/airports/kanto"}, Dates: meta.Dates{Uploaded: up, Modified: up}, Author: meta.Author{Extracted: "千葉 太郎"}}
	haneda := &meta.File{TenantID: "t1", FileID: "01B", Name: "羽田.docx", SizeBytes: 100,
		Tags: []string{"/airports/kanto", "/airports"}, Dates: meta.Dates{Uploaded: up.Add(time.Hour), Modified: up.Add(time.Hour)}}
	kansai := &meta.File{TenantID: "t1", FileID: "01C", Name: "関西.jpg", SizeBytes: 200,
		Tags: []string{"/airports/kansai"}, Dates: meta.Dates{Uploaded: up.Add(2 * time.Hour), Modified: up.Add(2 * time.Hour)}}
	docs := BuildDocs(narita, &extraction.Content{Pages: []extraction.PageText{
		{View: "main", Page: 1, Text: "表紙"},
		{View: "main", Page: 2, Text: "千葉県成田市にある日本最大の国際拠点空港である。"},
		{View: "main", Page: 3, Text: "空港コードはNRT。"},
	}}, DefaultLimits)
	docs = append(docs, BuildDocs(haneda, &extraction.Content{Texts: map[string]string{"extracted": "東京都大田区にある日本最大の空港。"}}, DefaultLimits)...)
	docs = append(docs, BuildDocs(kansai, nil, DefaultLimits)...)
	if err := c.Upsert(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	res, err := c.Search(ctx, SearchRequest{Tenant: "t1", Query: "成田市", WithPages: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].FileID != "01A" {
		t.Fatalf("expected narita first, got %+v", res.Hits)
	}
	if res.Hits[0].Page != 2 || res.Hits[0].Snippet == "" {
		t.Fatalf("expected page 2 with snippet, got %+v", res.Hits[0])
	}

	// A name-only match still returns the file, without a page.
	res, err = c.Search(ctx, SearchRequest{Tenant: "t1", Query: "関西", WithPages: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].FileID != "01C" || res.Hits[0].Page != 0 {
		t.Fatalf("name match: %+v", res.Hits)
	}

	// Author search.
	res, err = c.Search(ctx, SearchRequest{Tenant: "t1", Query: "千葉"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].FileID != "01A" {
		t.Fatalf("author search: %+v", res.Hits)
	}

	// Folder listing: exact membership of /tags/airports/kanto sorted by name.
	res, err = c.Search(ctx, SearchRequest{Tenant: "t1", Facets: []string{"/tags/airports/kanto"}, Exact: true, Sort: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || len(res.Hits) != 2 || res.Hits[0].FileID != "01A" || res.Hits[1].FileID != "01B" {
		t.Fatalf("folder listing: total %d hits %+v", res.Total, res.Hits)
	}
	// Prefix membership of /tags/airports includes every file below it.
	res, err = c.Search(ctx, SearchRequest{Tenant: "t1", Facets: []string{"/tags/airports"}, Sort: "-modified"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 || res.Hits[0].FileID != "01C" {
		t.Fatalf("prefix listing: total %d hits %+v", res.Total, res.Hits)
	}

	facets, err := c.Facets(ctx, FacetsRequest{Tenant: "t1", Path: "/tags/airports"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, ch := range facets.Children {
		counts[ch.Path] = ch.Count
	}
	if counts["/tags/airports/kanto"] != 2 || counts["/tags/airports/kansai"] != 1 {
		t.Fatalf("facet children %+v", facets.Children)
	}
	types, err := c.Facets(ctx, FacetsRequest{Tenant: "t1", Path: "/type"})
	if err != nil {
		t.Fatal(err)
	}
	if len(types.Children) != 3 {
		t.Fatalf("type facets %+v", types.Children)
	}

	// Deleting a file removes its pages too.
	if err := c.Delete(ctx, []string{"01A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	res, err = c.Search(ctx, SearchRequest{Tenant: "t1", Query: "成田市"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("deleted file still found: %+v", res.Hits)
	}
	st, err := c.Stats(ctx)
	if err != nil || st.NumDocs != 2 {
		t.Fatalf("stats %+v err %v", st, err)
	}
	// Tenant isolation.
	res, err = c.Search(ctx, SearchRequest{Tenant: "other", Query: "東京"})
	if err != nil || len(res.Hits) != 0 {
		t.Fatalf("tenant isolation: %+v err %v", res.Hits, err)
	}
}

func TestSidecarCommitFilesAndReadOnly(t *testing.T) {
	bin := testBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "index")
	w := New(bin, dir)
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.SchemaVersion() == "" {
		t.Fatal("schema version not reported by the ready line")
	}
	up := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	f := &meta.File{TenantID: "t1", FileID: "01A", Name: "成田国際空港の案内.pdf", Dates: meta.Dates{Uploaded: up, Modified: up}}
	docs := BuildDocs(f, &extraction.Content{Pages: []extraction.PageText{{View: "main", Page: 2, Text: "千葉県成田市にある空港。"}}}, DefaultLimits)
	if err := w.Upsert(ctx, docs); err != nil {
		t.Fatal(err)
	}
	committed, err := w.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if committed.NumDocs != 2 || committed.Opstamp == 0 || len(committed.Files) == 0 || committed.MetaJSON == "" {
		t.Fatalf("commit result: %+v", committed)
	}
	for _, name := range committed.Files {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.IsDir() {
			t.Fatalf("listed file %s does not exist: %v", name, err)
		}
	}
	listed, err := w.Files(ctx)
	if err != nil || listed.Opstamp != committed.Opstamp || len(listed.Files) != len(committed.Files) {
		t.Fatalf("files: %+v err %v", listed, err)
	}
	st, err := w.Stats(ctx)
	if err != nil || st.Opstamp != committed.Opstamp || st.ReadOnly {
		t.Fatalf("stats: %+v err %v", st, err)
	}

	// A read-only sidecar on the same directory searches but never writes.
	r := New(bin, dir)
	r.ReadOnly = true
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Upsert(ctx, docs); err == nil {
		t.Fatal("read-only sidecar accepted an upsert")
	}
	if n, err := r.Reload(ctx); err != nil || n != 2 {
		t.Fatalf("reload: %d %v", n, err)
	}
	res, err := r.Search(ctx, SearchRequest{Tenant: "t1", Query: "成田市", WithPages: true})
	if err != nil || len(res.Hits) != 1 || res.Hits[0].Page != 2 {
		t.Fatalf("read-only search: %+v err %v", res.Hits, err)
	}
	if st, err := r.Stats(ctx); err != nil || !st.ReadOnly {
		t.Fatalf("read-only stats: %+v err %v", st, err)
	}
}
