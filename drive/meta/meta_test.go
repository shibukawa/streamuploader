package meta

import (
	"context"
	"testing"
	"time"

	"streamuploader/drive/memstore"
)

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{" projects/2026 /alpha ", "/projects/2026/alpha", "b\\c", "//x//"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/b/c", "/projects/2026/alpha", "/x"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if _, err := NormalizeTag("   "); err == nil {
		t.Fatal("empty tag accepted")
	}
	if _, err := NormalizeTag("a\x00b"); err == nil {
		t.Fatal("control character accepted")
	}
}

func TestExtAndPrimaryDate(t *testing.T) {
	f := &File{Name: "レポート.PDF", ContentType: "application/pdf"}
	if f.Ext() != "pdf" {
		t.Fatalf("ext %q", f.Ext())
	}
	f = &File{Name: "noext", ContentType: "image/jpeg"}
	if ext := f.Ext(); ext != "jpg" {
		t.Fatalf("ext %q", ext)
	}
	shot := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	up := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	f = &File{Dates: Dates{Uploaded: up}}
	if !f.PrimaryDate().Equal(up) {
		t.Fatal("primary date should fall back to upload")
	}
	f.Dates.Shot = &shot
	if !f.PrimaryDate().Equal(shot) {
		t.Fatal("primary date should prefer shot")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	f := &File{TenantID: "t1", FileID: "01F", Name: "a.pdf", Tags: []string{"/a"}, Dates: Dates{Uploaded: time.Now().UTC()}}
	if err := WriteSnapshot(ctx, store, "b", "drive/", f); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	n, err := c.LoadSnapshots(ctx, store, "b", "drive/", "t1")
	if err != nil || n != 1 {
		t.Fatalf("loaded %d err %v", n, err)
	}
	got, ok := c.Get("01F")
	if !ok || got.Name != "a.pdf" || got.Tags[0] != "/a" {
		t.Fatalf("cache miss or mismatch: %+v", got)
	}
	clone := got.Clone()
	clone.Tags[0] = "/changed"
	if got.Tags[0] != "/a" {
		t.Fatal("Clone aliases tags")
	}
}
