package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"streamuploader/drive/journal"
	"streamuploader/drive/memstore"
	"streamuploader/drive/meta"
	"streamuploader/internal/storage"
)

func appendEvents(t *testing.T, js *journal.Store, n int, typ journal.EventType) []string {
	t.Helper()
	var keys []string
	for i := 0; i < n; i++ {
		id := "f" + string(rune('a'+i))
		ev := &journal.Event{TenantID: "t1", Type: typ, FileID: id}
		if typ != journal.FileAccessed {
			sum := sha256.Sum256([]byte("hello"))
			ev.State = &meta.File{TenantID: "t1", FileID: id, Name: id + ".txt", ObjectKey: "uploads/" + id + ".txt", ChecksumSHA256: hex.EncodeToString(sum[:]), SizeBytes: 5, Tags: []string{}}
		} else {
			ev.Access = &journal.Access{ObjectKey: "uploads/" + id + ".txt", Kind: "download"}
		}
		key, err := js.Append(context.Background(), ev)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	return keys
}

func TestChainAppendAndVerify(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	lock := storage.LockPolicy{Mode: storage.LockModeLegalHold}
	js := &journal.Store{Objects: store, Bucket: "b", Prefix: "drive/", Lock: lock}
	chain := &Chain{Objects: store, Bucket: "b", Prefix: "drive/", Tenant: "t1", Lock: lock}
	for i := 0; i < 3; i++ {
		_, _ = store.PutObject(ctx, storage.PutInput{Bucket: "b", Key: "uploads/f" + string(rune('a'+i)) + ".txt", Body: bytes.NewReader([]byte("hello"))})
	}
	keys := appendEvents(t, js, 3, journal.FileCreated)

	head, err := chain.Head(ctx)
	if err != nil || head.N != 0 || head.SHA256 != Genesis("t1") {
		t.Fatalf("empty head = %+v, %v", head, err)
	}
	entries, err := js.ListRange(ctx, "t1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	head, cp, err := chain.Append(ctx, head, entries[:2], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cp.N != 1 || cp.EventCount != 2 || len(cp.Originals) != 2 || cp.JournalRange.Last != keys[1] || cp.PrevCheckpointSHA256 != Genesis("t1") {
		t.Fatalf("checkpoint 1: %+v", cp)
	}
	if head, cp, err = chain.Append(ctx, head, entries[2:], time.Now()); err != nil || cp.N != 2 || head.N != 2 {
		t.Fatalf("checkpoint 2: %+v %v", cp, err)
	}
	// Checkpoints are locked like the journal.
	if r := store.Retention("b", Key("drive/", "t1", 1)); r == nil || !r.LegalHold {
		t.Fatalf("checkpoint not locked: %+v", r)
	}
	// Re-reading the head from the bucket gives the same chain end.
	reloaded, err := chain.Head(ctx)
	if err != nil || reloaded != head {
		t.Fatalf("reloaded head %+v want %+v (%v)", reloaded, head, err)
	}
	// An unfolded tail is reported but is not a failure.
	appendEvents(t, js, 1, journal.FileAccessed)
	rep, err := chain.Verify(ctx, js, VerifyOptions{HashOriginals: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || rep.Checkpoints != 2 || rep.EventsVerified != 3 || rep.OriginalsVerified != 3 || rep.UnverifiedTail != 1 {
		t.Fatalf("clean verify: %+v", rep)
	}

	// The lock refuses an API delete; an operator removing the object out
	// of band is caught by the next verification, which names the event.
	if err := store.DeleteObject(ctx, storage.DeleteInput{Bucket: "b", Key: keys[1]}); err == nil {
		t.Fatal("locked journal object was deleted")
	}
	store.Remove("b", keys[1])
	rep, _ = chain.Verify(ctx, js, VerifyOptions{})
	if rep.OK() || len(rep.Findings) != 1 || rep.Findings[0].Kind != FindingMissingEvent || rep.Findings[0].Key != keys[1] || rep.Findings[0].Checkpoint != 1 {
		t.Fatalf("missing event not named: %+v", rep.Findings)
	}

	// A rewritten event changes its digest.
	store.Remove("b", keys[2])
	_, _ = store.PutObject(ctx, storage.PutInput{Bucket: "b", Key: keys[2], Body: strings.NewReader(`{"event_id":"forged"}`)})
	rep, _ = chain.Verify(ctx, js, VerifyOptions{})
	kinds := map[string]string{}
	for _, f := range rep.Findings {
		kinds[f.Kind] = f.Key
	}
	if kinds[FindingModifiedEvent] != keys[2] || kinds[FindingMissingEvent] != keys[1] {
		t.Fatalf("findings: %+v", rep.Findings)
	}

	// A missing or altered original is reported too.
	store.Remove("b", "uploads/fa.txt")
	store.Remove("b", "uploads/fb.txt")
	_, _ = store.PutObject(ctx, storage.PutInput{Bucket: "b", Key: "uploads/fb.txt", Body: strings.NewReader("hellO")})
	rep, _ = chain.Verify(ctx, js, VerifyOptions{HashOriginals: true})
	kinds = map[string]string{}
	for _, f := range rep.Findings {
		kinds[f.Kind] = f.Key
	}
	if kinds[FindingMissingOriginal] != "uploads/fa.txt" || kinds[FindingModifiedOriginal] != "uploads/fb.txt" {
		t.Fatalf("original findings: %+v", rep.Findings)
	}

	// A chain whose link was rewritten breaks.
	cp2, _, _ := chain.Read(ctx, 2)
	cp2.PrevCheckpointSHA256 = Genesis("t1")
	store.Remove("b", Key("drive/", "t1", 2))
	raw, _ := jsonMarshal(cp2)
	_, _ = store.PutObject(ctx, storage.PutInput{Bucket: "b", Key: Key("drive/", "t1", 2), Body: bytes.NewReader(raw)})
	rep, _ = chain.Verify(ctx, js, VerifyOptions{})
	found := false
	for _, f := range rep.Findings {
		if f.Kind == FindingChainBreak && f.Checkpoint == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("chain break not reported: %+v", rep.Findings)
	}
}

func TestMerkleRootAndKeys(t *testing.T) {
	a := Leaf{Key: "a", SHA256: strings.Repeat("ab", 32)}
	b := Leaf{Key: "b", SHA256: strings.Repeat("cd", 32)}
	if MerkleRoot([]Leaf{a}) != a.SHA256 {
		t.Fatal("single leaf root must be the leaf")
	}
	if MerkleRoot([]Leaf{a, b}) == MerkleRoot([]Leaf{b, a}) {
		t.Fatal("root must depend on order")
	}
	if MerkleRoot([]Leaf{a, b, a}) == MerkleRoot([]Leaf{a, b}) {
		t.Fatal("root must depend on count")
	}
	key := Key("drive/", "t1", 7)
	if key != "drive/audit/t1/checkpoint-000000000007.json" {
		t.Fatalf("key %q", key)
	}
	if n, ok := ParseN(key); !ok || n != 7 {
		t.Fatalf("ParseN(%q) = %d, %v", key, n, ok)
	}
	if _, ok := ParseN("drive/audit/t1/other.json"); ok {
		t.Fatal("foreign key parsed")
	}
}
