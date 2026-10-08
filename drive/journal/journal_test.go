package journal

import (
	"context"
	"sort"
	"testing"
	"time"

	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
)

func TestULIDOrdersInTime(t *testing.T) {
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	ids := []string{
		NewULID(base),
		NewULID(base),
		NewULID(base.Add(time.Millisecond)),
		NewULID(base.Add(time.Second)),
	}
	for i := 1; i < len(ids); i++ {
		if !(ids[i-1] < ids[i]) {
			t.Fatalf("ids not increasing: %q then %q", ids[i-1], ids[i])
		}
	}
	for _, id := range ids {
		if len(id) != 26 {
			t.Fatalf("ulid %q has length %d", id, len(id))
		}
	}
	got, err := ULIDTime(ids[3])
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(base.Add(time.Second)) {
		t.Fatalf("decoded time %v, want %v", got, base.Add(time.Second))
	}
}

func TestKeysSortChronologically(t *testing.T) {
	times := []time.Time{
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2027, 1, 1, 0, 0, 1, 0, time.UTC),
		time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	var keys []string
	for _, at := range times {
		keys = append(keys, Key("drive/", "t1", NewULID(at), at))
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for i := range keys {
		if keys[i] != sorted[i] {
			t.Fatalf("keys are not in time order: %v", keys)
		}
	}
}

func TestAppendAndListAfter(t *testing.T) {
	ctx := context.Background()
	store := &Store{Objects: memstore.New(), Bucket: "b", Prefix: "drive/"}
	var keys []string
	for i := 0; i < 3; i++ {
		key, err := store.Append(ctx, &Event{
			TenantID: "t1",
			Type:     FileCreated,
			FileID:   "f" + string(rune('a'+i)),
			State:    &meta.File{TenantID: "t1", FileID: "f" + string(rune('a'+i)), Name: "n", Tags: []string{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	all, err := store.ListAfter(ctx, "t1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d events, want 3", len(all))
	}
	if all[0].Event.State.LastEvent != keys[0] {
		t.Fatalf("state.last_event = %q, want %q", all[0].Event.State.LastEvent, keys[0])
	}
	after, err := store.ListAfter(ctx, "t1", keys[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].Key != keys[1] {
		t.Fatalf("ListAfter returned %d entries starting %q", len(after), after[0].Key)
	}
	limited, err := store.ListAfter(ctx, "t1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("MaxKeys ignored: %d entries", len(limited))
	}
	other, err := store.ListAfter(ctx, "t2", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("tenant isolation broken: %d entries", len(other))
	}
}
