package indexer

import (
	"strings"
	"time"

	"streamuploader/drive/meta"
	"streamuploader/internal/extraction"
)

// applyExtractedMetadata copies what the extracted content knows about the
// document into the file state: the author (Dublin Core creator, then PDF
// Info Author, then EXIF Artist) and the creation time. User overrides are
// never touched.
func applyExtractedMetadata(f *meta.File, content *extraction.Content) {
	if content == nil {
		return
	}
	if author := extractedAuthor(content); author != "" {
		f.Author.Extracted = author
	}
	if created := extractedCreated(content); created != nil && (f.Dates.Created == nil || f.Dates.Created.IsZero()) {
		f.Dates.Created = created
	}
}

func extractedAuthor(content *extraction.Content) string {
	if doc, ok := content.Metadata["document"].(map[string]any); ok {
		if dc, ok := doc["dc"].(map[string]any); ok {
			if v := firstString(dc["creator"]); v != "" {
				return v
			}
		}
	}
	for _, key := range []string{"author", "Author", "creator"} {
		if v := firstString(content.Metadata[key]); v != "" {
			return v
		}
	}
	if exif, ok := content.Metadata["exif"].(map[string]any); ok {
		for _, key := range []string{"Artist", "artist"} {
			if v := firstString(exif[key]); v != "" {
				return v
			}
		}
	}
	return ""
}

func extractedCreated(content *extraction.Content) *time.Time {
	var raw string
	if doc, ok := content.Metadata["document"].(map[string]any); ok {
		if dc, ok := doc["dc"].(map[string]any); ok {
			raw = firstString(dc["created"])
		}
	}
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// firstString reads a string or the first element of a string list.
func firstString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	case []string:
		for _, s := range x {
			if strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
