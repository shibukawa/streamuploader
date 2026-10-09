// Package snapshot publishes the tantivy index to the bucket and follows it
// from there, so that servers and the indexer can run as separate processes.
//
// Layout under {prefix}search/{tenant}/ (policy:drive-s3-layout):
//
//	meta.json        the pointer: tantivy's meta.json plus a "drive" object
//	{segment}.{ext}  immutable segment files, named by tantivy
//
// Segment files are uploaded first and the pointer last, so a reader never
// sees a pointer to a missing file. Files a new pointer no longer references
// are recorded as retired and deleted after a grace period longer than any
// reader's refresh interval. See .knowledge/concepts/data/search-index-snapshot.yaml.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// PointerName is the object name of the pointer inside the tenant prefix.
const PointerName = "meta.json"

const (
	maxHistoryGenerations = 64
	maxHistoryIDs         = 5000
)

// FileRef is one segment file of the published index.
type FileRef struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Retired is a file an earlier pointer referenced and the current one does
// not; it is deleted once At is older than the grace period.
type Retired struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

// Change lists the file ids whose index documents and meta snapshots changed
// in one generation, so a follower can refresh its meta cache without
// re-reading every snapshot. Full means everything changed (a rebuild).
type Change struct {
	Generation int64    `json:"generation"`
	Full       bool     `json:"full,omitempty"`
	Changed    []string `json:"changed,omitempty"`
}

// Pointer is the "drive" object of search/{tenant}/meta.json. Meta holds the
// rest of the object, which is tantivy's own meta.json and is written to the
// local index directory verbatim.
type Pointer struct {
	SchemaVersion  string            `json:"schema_version"`
	Tokenizer      string            `json:"tokenizer,omitempty"`
	Generation     int64             `json:"generation"`
	Opstamp        uint64            `json:"opstamp"`
	PublishedAt    time.Time         `json:"published_at"`
	LastJournalKey string            `json:"last_journal_key"`
	PendingText    map[string]string `json:"pending_text,omitempty"`
	NumDocs        int64             `json:"num_docs"`
	Files          []FileRef         `json:"files"`
	Retired        []Retired         `json:"retired,omitempty"`
	History        []Change          `json:"history,omitempty"`

	Meta json.RawMessage `json:"-"`
}

// segmentName matches the files tantivy writes for a segment: a 32-digit hex
// uuid followed by an extension such as .idx, .store or .12.del.
var segmentName = regexp.MustCompile(`^[0-9a-f]{32}\.[A-Za-z0-9.]+$`)

// IsSegmentFile reports whether name is a tantivy segment file name, which
// is also the only kind of name the snapshot ever uploads or deletes.
func IsSegmentFile(name string) bool {
	return segmentName.MatchString(name)
}

// Encode writes the pointer object: tantivy's meta.json fields plus "drive".
func Encode(p *Pointer) ([]byte, error) {
	obj := map[string]json.RawMessage{}
	if len(p.Meta) > 0 {
		if err := json.Unmarshal(p.Meta, &obj); err != nil {
			return nil, fmt.Errorf("snapshot: tantivy meta is not an object: %w", err)
		}
	}
	drive, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	obj["drive"] = drive
	return json.MarshalIndent(obj, "", "  ")
}

// Decode parses a pointer object and separates tantivy's fields into Meta.
func Decode(body []byte) (*Pointer, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("snapshot: parse pointer: %w", err)
	}
	raw, ok := obj["drive"]
	if !ok {
		return nil, errors.New("snapshot: pointer has no drive section")
	}
	var p Pointer
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("snapshot: parse drive section: %w", err)
	}
	delete(obj, "drive")
	meta, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	p.Meta = meta
	if p.PendingText == nil {
		p.PendingText = map[string]string{}
	}
	for _, f := range p.Files {
		if !IsSegmentFile(f.Name) {
			return nil, fmt.Errorf("snapshot: pointer lists a non-segment file %q", f.Name)
		}
	}
	return &p, nil
}

// FileSet indexes the referenced files by name.
func (p *Pointer) FileSet() map[string]FileRef {
	out := make(map[string]FileRef, len(p.Files))
	for _, f := range p.Files {
		out[f.Name] = f
	}
	return out
}

// ChangesSince tells a follower at generation gen what to refresh: the file
// ids changed in (gen, p.Generation], or full when a rebuild happened in
// between. ok is false when the history no longer covers gen, in which case
// the follower reloads everything.
func (p *Pointer) ChangesSince(gen int64) (ids []string, full bool, ok bool) {
	if gen == p.Generation {
		return nil, false, true
	}
	if gen > p.Generation {
		// The pointer went backwards (restored or rewritten); start over.
		return nil, true, true
	}
	byGen := make(map[int64]Change, len(p.History))
	for _, c := range p.History {
		byGen[c.Generation] = c
	}
	seen := map[string]bool{}
	for g := gen + 1; g <= p.Generation; g++ {
		c, found := byGen[g]
		if !found {
			return nil, false, false
		}
		if c.Full {
			return nil, true, true
		}
		for _, id := range c.Changed {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids, false, true
}

// appendHistory adds the change of a new generation and trims the history to
// its bounds. A full change makes the older entries useless.
func appendHistory(history []Change, c Change) []Change {
	if c.Full {
		return []Change{{Generation: c.Generation, Full: true}}
	}
	out := append(append([]Change(nil), history...), c)
	total := 0
	for _, h := range out {
		total += len(h.Changed)
	}
	for len(out) > 1 && (len(out) > maxHistoryGenerations || total > maxHistoryIDs) {
		total -= len(out[0].Changed)
		out = out[1:]
	}
	return out
}
