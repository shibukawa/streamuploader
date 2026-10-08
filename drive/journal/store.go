package journal

import (
	"bytes"
	"context"
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
	// Now is replaceable for tests.
	Now func() time.Time
}

// Entry is a listed event with its object key.
type Entry struct {
	Key   string
	Event *Event
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
		ev, err := s.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Key: key, Event: ev})
	}
	return entries, nil
}

// Get reads one event.
func (s *Store) Get(ctx context.Context, key string) (*Event, error) {
	out, err := s.Objects.GetObject(ctx, storage.GetInput{Bucket: s.Bucket, Key: key})
	if err != nil {
		return nil, fmt.Errorf("journal: get %s: %w", key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("journal: parse %s: %w", key, err)
	}
	return &ev, nil
}
