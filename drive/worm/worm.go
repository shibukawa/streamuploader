// Package worm defines the write-once-read-many modes of the Drive and the
// rule that decides which metadata changes an append-only deployment still
// accepts. See .knowledge/concepts/requirement/local-drive-worm-audit-mode.yaml
// and policy/worm-enforcement.yaml.
package worm

import (
	"fmt"
	"strings"

	"streamuploader/drive/meta"
)

// Mode is the WORM variant of a deployment.
type Mode string

const (
	// Off is the normal Drive: metadata is editable and files can be deleted.
	Off Mode = "off"
	// AppendOnly accepts additions (new tags, a first author or location) as
	// new journal events; nothing already recorded is removed or rewritten.
	AppendOnly Mode = "append_only"
	// Strict accepts no metadata change after registration; only new files
	// and new versions.
	Strict Mode = "strict"
)

// ParseMode reads a configuration value; empty means Off.
func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "false", "0", "none":
		return Off, nil
	case "append_only", "append-only", "appendonly":
		return AppendOnly, nil
	case "strict", "true", "1", "on":
		return Strict, nil
	}
	return Off, fmt.Errorf("unknown WORM mode %q (off, append_only, strict)", value)
}

// Enabled reports whether any WORM rule applies.
func (m Mode) Enabled() bool { return m == AppendOnly || m == Strict }

func (m Mode) String() string {
	if m == "" {
		return string(Off)
	}
	return string(m)
}

// ErrReadonly is the reason a change was refused; the API maps it to 405
// with code worm_readonly.
type ErrReadonly struct {
	Reason string
}

func (e *ErrReadonly) Error() string { return "worm: " + e.Reason }

// CheckUpdate decides whether an in-place metadata change from cur to next is
// allowed under the mode. Strict refuses every change; AppendOnly refuses
// anything that removes or rewrites recorded values.
func (m Mode) CheckUpdate(cur, next *meta.File) error {
	switch m {
	case Off:
		return nil
	case Strict:
		return &ErrReadonly{Reason: "metadata cannot change after registration in strict WORM mode; upload a new version instead"}
	}
	if cur.Name != next.Name {
		return &ErrReadonly{Reason: "the name cannot change in append-only WORM mode"}
	}
	if cur.Author.Override != "" && cur.Author.Override != next.Author.Override {
		return &ErrReadonly{Reason: "the author override is already recorded and cannot change"}
	}
	if cur.Location != nil {
		if next.Location == nil {
			return &ErrReadonly{Reason: "the location cannot be cleared in append-only WORM mode"}
		}
		if cur.Location.Lat != next.Location.Lat || cur.Location.Lon != next.Location.Lon {
			return &ErrReadonly{Reason: "the location is already recorded and cannot change"}
		}
	}
	have := make(map[string]bool, len(next.Tags))
	for _, t := range next.Tags {
		have[t] = true
	}
	for _, t := range cur.Tags {
		if !have[t] {
			return &ErrReadonly{Reason: "tag " + t + " cannot be removed in append-only WORM mode"}
		}
	}
	return nil
}
