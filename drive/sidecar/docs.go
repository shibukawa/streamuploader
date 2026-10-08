package sidecar

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"streamuploader/drive/meta"
	"streamuploader/internal/extraction"
)

// Limits bound what one file contributes to the index.
type Limits struct {
	// MaxBodyBytes truncates the joined text indexed on the file document.
	MaxBodyBytes int
	// MaxPageBytes truncates each page's stored text.
	MaxPageBytes int
	// MaxPages drops pages beyond this count.
	MaxPages int
}

var DefaultLimits = Limits{MaxBodyBytes: 512 << 10, MaxPageBytes: 64 << 10, MaxPages: 2000}

// BuildDocs turns a file state and its extracted content into index
// documents: one file document and one document per page.
func BuildDocs(f *meta.File, content *extraction.Content, limits Limits) []Doc {
	if limits.MaxBodyBytes <= 0 {
		limits = DefaultLimits
	}
	file := Doc{
		ID:     "f:" + f.FileID,
		Kind:   "file",
		Tenant: f.TenantID,
		FileID: f.FileID,
		Name:   f.Name,
		Ext:    f.Ext(),
		Author: f.Author.Effective(),
		Facets: FacetPaths(f),
	}
	size := f.SizeBytes
	file.Size = &size
	if !f.Dates.Uploaded.IsZero() {
		file.Uploaded = unixPtr(f.Dates.Uploaded.Unix())
	}
	if !f.Dates.Modified.IsZero() {
		file.Modified = unixPtr(f.Dates.Modified.Unix())
	} else if !f.Dates.Uploaded.IsZero() {
		file.Modified = unixPtr(f.Dates.Uploaded.Unix())
	}
	if f.Dates.Created != nil && !f.Dates.Created.IsZero() {
		file.Created = unixPtr(f.Dates.Created.Unix())
	}
	if f.Location != nil {
		lat, lon := f.Location.Lat, f.Location.Lon
		file.Lat, file.Lon = &lat, &lon
	}
	docs := []Doc{file}
	if content == nil {
		return docs
	}
	var body strings.Builder
	for _, key := range []string{"title", "description", "extracted", "text", "ocr"} {
		if t := strings.TrimSpace(content.Texts[key]); t != "" {
			body.WriteString(t)
			body.WriteString("\n")
		}
	}
	if body.Len() == 0 {
		for _, p := range content.Pages {
			body.WriteString(p.Text)
			body.WriteString("\n")
		}
	}
	docs[0].Body = truncateUTF8(body.String(), limits.MaxBodyBytes)
	for i, p := range content.Pages {
		if i >= limits.MaxPages {
			break
		}
		text := truncateUTF8(p.Text, limits.MaxPageBytes)
		if strings.TrimSpace(text) == "" {
			continue
		}
		page := int64(p.Page)
		docs = append(docs, Doc{
			ID:     fmt.Sprintf("p:%s:%s:%d", f.FileID, p.View, p.Page),
			Kind:   "page",
			Tenant: f.TenantID,
			FileID: f.FileID,
			Body:   text,
			Page:   &page,
			View:   p.View,
		})
	}
	return docs
}

// FacetPaths lists every virtual folder the file appears in: its tags under
// /tags, the type, the date, the author and the geohash levels.
func FacetPaths(f *meta.File) []string {
	paths := make([]string, 0, len(f.Tags)+4)
	for _, tag := range f.Tags {
		paths = append(paths, "/tags"+tag)
	}
	if ext := f.Ext(); ext != "" {
		paths = append(paths, "/type/"+facetSegment(ext))
	}
	d := f.PrimaryDate()
	if !d.IsZero() {
		d = d.UTC()
		paths = append(paths, fmt.Sprintf("/date/%04d/%02d/%02d", d.Year(), int(d.Month()), d.Day()))
	}
	if author := f.Author.Effective(); author != "" {
		paths = append(paths, "/author/"+facetSegment(author))
	}
	if f.Location != nil {
		hash := f.Location.Geohash
		if hash == "" {
			hash = Geohash(f.Location.Lat, f.Location.Lon, 8)
		}
		var b strings.Builder
		b.WriteString("/geo")
		for _, r := range hash {
			b.WriteByte('/')
			b.WriteRune(r)
		}
		paths = append(paths, b.String())
	}
	return paths
}

// facetSegment makes a value safe as one facet path element.
func facetSegment(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "/", "／")
	if s == "" {
		return "-"
	}
	return s
}

func unixPtr(v int64) *int64 { return &v }

func truncateUTF8(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
