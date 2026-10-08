package meta

import (
	"errors"
	"sort"
	"strings"
	"unicode"
)

// ErrInvalidTag is returned for tags that cannot become a facet path.
var ErrInvalidTag = errors.New("invalid tag")

// NormalizeTag turns user input such as "projects/2026/alpha " into the
// canonical "/projects/2026/alpha". Empty segments are dropped and control
// characters are refused.
func NormalizeTag(raw string) (string, error) {
	parts := strings.Split(strings.ReplaceAll(raw, "\\", "/"), "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return "", ErrInvalidTag
			}
		}
		segments = append(segments, part)
	}
	if len(segments) == 0 {
		return "", ErrInvalidTag
	}
	return "/" + strings.Join(segments, "/"), nil
}

// NormalizeTags normalizes, deduplicates and sorts a tag list.
func NormalizeTags(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		tag, err := NormalizeTag(r)
		if err != nil {
			return nil, err
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out, nil
}

// HasTag reports whether the file carries exactly this tag (not a parent).
func (f *File) HasTag(tag string) bool {
	for _, t := range f.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// HasTagUnder reports whether the file carries the tag or a descendant of it.
func (f *File) HasTagUnder(prefix string) bool {
	for _, t := range f.Tags {
		if t == prefix || strings.HasPrefix(t, prefix+"/") {
			return true
		}
	}
	return false
}
