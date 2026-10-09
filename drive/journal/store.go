package journal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"streamuploader/internal/storage"
)

// Store appends and lists events on an object store.
type Store struct {
	Objects storage.Store
	Bucket  string
	// Prefix is the root of all Drive keys in the bucket, such as "drive/".
	Prefix string
	// Lock is attached to every event written; in WORM mode it is the
	// object lock that keeps the journal from being deleted.
	Lock storage.LockPolicy
	// Now is replaceable for tests.
	Now func() time.Time
}

// Entry is a listed event with its object key and the SHA-256 of the stored
// bytes, which the audit checkpoint chain covers.
type Entry struct {
	Key    string
	Digest string
	Event  *Event
}

// Append writes one event and returns its key. EventID and At are filled in
// when empty. A write never reads first, so concurrent writers cannot conflict.
func (s *Store) Append(ctx context.Context, ev *Event) (string, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if ev.At.IsZero() {
		ev.At = now().UTC()
	}
	if ev.EventID == "" {
		ev.EventID = NewULID(ev.At)
	}
	if ev.TenantID == "" {
		return "", fmt.Errorf("journal: event has no tenant")
	}
	if ev.State != nil {
		ev.State.LastEvent = Key(s.Prefix, ev.TenantID, ev.EventID, ev.At)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return "", err
	}
	key := Key(s.Prefix, ev.TenantID, ev.EventID, ev.At)
	_, err = s.Objects.PutObject(ctx, storage.PutInput{
		Bucket:      s.Bucket,
		Key:         key,
		Body:        bytes.NewReader(body),
		ContentType: "application/json",
		Retention:   s.Lock.For(now()),
	})
	if err != nil {
		return "", fmt.Errorf("journal: put %s: %w", key, err)
	}
	return key, nil
}

// ListAfter returns up to max events whose key sorts after afterKey, oldest
// first. An empty afterKey starts from the beginning.
func (s *Store) ListAfter(ctx context.Context, tenant, afterKey string, max int) ([]Entry, error) {
	list, err := s.Objects.ListObjects(ctx, storage.ListInput{
		Bucket:     s.Bucket,
		Prefix:     Prefix(s.Prefix, tenant),
		StartAfter: afterKey,
		MaxKeys:    max,
	})
	if err != nil {
		return nil, fmt.Errorf("journal: list: %w", err)
	}
	entries := make([]Entry, 0, len(list.Keys))
	for _, key := range list.Keys {
		e, err := s.Read(ctx, key)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ListRange returns every event whose key sorts after afterKey and at or
// before untilKey, oldest first, paging through the listing as needed. An
// empty untilKey means no upper bound.
func (s *Store) ListRange(ctx context.Context, tenant, afterKey, untilKey string) ([]Entry, error) {
	const page = 1000
	var entries []Entry
	cursor := afterKey
	for {
		keys, err := s.ListKeys(ctx, tenant, cursor, page)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if untilKey != "" && key > untilKey {
				return entries, nil
			}
			e, err := s.Read(ctx, key)
			if err != nil {
				return nil, err
			}
			entries = append(entries, e)
			cursor = key
		}
		if len(keys) < page {
			return entries, nil
		}
	}
}

// ListKeys lists event keys after afterKey without reading the events.
func (s *Store) ListKeys(ctx context.Context, tenant, afterKey string, max int) ([]string, error) {
	list, err := s.Objects.ListObjects(ctx, storage.ListInput{
		Bucket:     s.Bucket,
		Prefix:     Prefix(s.Prefix, tenant),
		StartAfter: afterKey,
		MaxKeys:    max,
	})
	if err != nil {
		return nil, fmt.Errorf("journal: list: %w", err)
	}
	return list.Keys, nil
}

// Get reads one event.
func (s *Store) Get(ctx context.Context, key string) (*Event, error) {
	e, err := s.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	return e.Event, nil
}

// Read reads one event together with the digest of its stored bytes.
func (s *Store) Read(ctx context.Context, key string) (Entry, error) {
	entry, _, err := s.ReadRaw(ctx, key)
	return entry, err
}

// ReadRaw is Read that also hands back the stored bytes, for an export that
// forwards the event exactly as the bucket holds it.
func (s *Store) ReadRaw(ctx context.Context, key string) (Entry, []byte, error) {
	out, err := s.Objects.GetObject(ctx, storage.GetInput{Bucket: s.Bucket, Key: key})
	if err != nil {
		return Entry{}, nil, fmt.Errorf("journal: get %s: %w", key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, 8<<20))
	if err != nil {
		return Entry{}, nil, err
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return Entry{}, nil, fmt.Errorf("journal: parse %s: %w", key, err)
	}
	return Entry{Key: key, Digest: Digest(body), Event: &ev}, body, nil
}

// Digest is the hex SHA-256 of an event object's bytes, the leaf value of
// the audit checkpoint chain.
func Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
