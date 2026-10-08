// Package journal is the append-only record of Drive metadata changes. Every
// change is one immutable JSON object under journal/{tenant}/{yyyy}/{mm}/{ulid}.json
// carrying the file's full state after the change. See
// .knowledge/concepts/data/drive-journal-event.yaml.
package journal

import (
	"encoding/json"
	"fmt"
	"time"

	"streamuploader/drive/meta"
)

type EventType string

const (
	FileCreated   EventType = "file.created"
	FileUpdated   EventType = "file.updated"
	FileVersioned EventType = "file.versioned"
	FileDeleted   EventType = "file.deleted"
	FileRestored  EventType = "file.restored"
	FileAccessed  EventType = "file.accessed"
	ViewSaved     EventType = "view.saved"
	ViewDeleted   EventType = "view.deleted"
)

// Event is one journal entry.
type Event struct {
	EventID          string          `json:"event_id"`
	TenantID         string          `json:"tenant_id"`
	Type             EventType       `json:"type"`
	Actor            string          `json:"actor,omitempty"`
	At               time.Time       `json:"at"`
	FileID           string          `json:"file_id,omitempty"`
	State            *meta.File      `json:"state,omitempty"`
	Access           *Access         `json:"access,omitempty"`
	ExpectedRevision int64           `json:"expected_revision,omitempty"`
	UploadFacts      json.RawMessage `json:"upload_facts,omitempty"`
}

// Access describes a read in WORM mode.
type Access struct {
	ObjectKey  string `json:"object_key"`
	Kind       string `json:"kind"`
	ClientHash string `json:"client_hash,omitempty"`
}

// IsFileState reports whether the event carries a file state the indexer folds.
func (e *Event) IsFileState() bool {
	switch e.Type {
	case FileCreated, FileUpdated, FileVersioned, FileDeleted, FileRestored:
		return e.State != nil
	}
	return false
}

// Key is the object key of an event: journal/{tenant}/{yyyy}/{mm}/{ulid}.json.
// The month folder keeps listings bounded; the ULID orders events in time.
func Key(prefix, tenant, eventID string, at time.Time) string {
	at = at.UTC()
	return fmt.Sprintf("%sjournal/%s/%04d/%02d/%s.json", prefix, tenant, at.Year(), int(at.Month()), eventID)
}

// Prefix lists every event of a tenant.
func Prefix(prefix, tenant string) string {
	return prefix + "journal/" + tenant + "/"
}
