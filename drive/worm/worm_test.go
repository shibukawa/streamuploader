package worm

import (
	"testing"

	"streamuploader/drive/meta"
)

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Off, "off": Off, "append_only": AppendOnly, "APPEND-ONLY": AppendOnly, "strict": Strict, "true": Strict} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("sometimes"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestCheckUpdate(t *testing.T) {
	base := func() *meta.File {
		return &meta.File{Name: "a.pdf", Tags: []string{"/inbox"}, Author: meta.Author{Override: ""}}
	}
	cur := base()
	if err := Off.CheckUpdate(cur, &meta.File{Name: "b.pdf"}); err != nil {
		t.Fatalf("off refused: %v", err)
	}
	if err := Strict.CheckUpdate(cur, base()); err == nil {
		t.Fatal("strict accepted an update")
	}
	cases := []struct {
		name string
		next func(*meta.File)
		ok   bool
	}{
		{"add tag", func(f *meta.File) { f.Tags = append(f.Tags, "/projects/x") }, true},
		{"remove tag", func(f *meta.File) { f.Tags = nil }, false},
		{"rename", func(f *meta.File) { f.Name = "b.pdf" }, false},
		{"first author", func(f *meta.File) { f.Author.Override = "me" }, true},
		{"first location", func(f *meta.File) { f.Location = &meta.Location{Lat: 1, Lon: 2} }, true},
	}
	for _, c := range cases {
		next := base()
		c.next(next)
		err := AppendOnly.CheckUpdate(cur, next)
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
	withAuthor := base()
	withAuthor.Author.Override = "me"
	changed := base()
	changed.Author.Override = "you"
	if err := AppendOnly.CheckUpdate(withAuthor, changed); err == nil {
		t.Fatal("author rewrite accepted")
	}
	withLoc := base()
	withLoc.Location = &meta.Location{Lat: 1, Lon: 2}
	if err := AppendOnly.CheckUpdate(withLoc, base()); err == nil {
		t.Fatal("location clear accepted")
	}
}
