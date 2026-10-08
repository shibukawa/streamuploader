// Package audit is the tamper-evidence layer of WORM mode: a hash-chained
// checkpoint written by the indexer after every fold, covering the journal
// events it folded and the originals they registered, plus the verifier
// that walks the chain. See .knowledge/concepts/data/audit-checkpoint.yaml.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"streamuploader/drive/journal"
	"streamuploader/internal/storage"
)

// Range is the first and last journal key a checkpoint covers.
type Range struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// Leaf is one covered journal object: its key and the SHA-256 of its bytes.
// Leaves let the verifier name a missing or modified event, which the
// Merkle root alone cannot.
type Leaf struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
}

// Original is an object registered by a file.created or file.versioned
// event in the covered range.
type Original struct {
	FileID         string `json:"file_id"`
	ObjectKey      string `json:"object_key"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
}

// Checkpoint is one link of the chain at audit/{tenant}/checkpoint-{n}.json.
type Checkpoint struct {
	N                    int64      `json:"n"`
	TenantID             string     `json:"tenant_id"`
	At                   time.Time  `json:"at"`
	PrevCheckpointSHA256 string     `json:"prev_checkpoint_sha256"`
	JournalRange         Range      `json:"journal_range"`
	JournalMerkleRoot    string     `json:"journal_merkle_root"`
	EventCount           int        `json:"event_count"`
	Leaves               []Leaf     `json:"leaves"`
	Originals            []Original `json:"originals,omitempty"`
	IndexMetaSHA256      string     `json:"index_meta_sha256,omitempty"`
	Anchor               string     `json:"anchor,omitempty"`
	SignedBy             string     `json:"signed_by,omitempty"`
}

// Key is the object key of checkpoint n. The number is zero-padded so a
// listing returns checkpoints in order.
func Key(prefix, tenant string, n int64) string {
	return fmt.Sprintf("%saudit/%s/checkpoint-%012d.json", prefix, tenant, n)
}

// KeyPrefix lists every checkpoint of a tenant.
func KeyPrefix(prefix, tenant string) string {
	return prefix + "audit/" + tenant + "/"
}

// ParseN reads the sequence number out of a checkpoint key.
func ParseN(key string) (int64, bool) {
	base := key[strings.LastIndex(key, "/")+1:]
	num, ok := strings.CutPrefix(base, "checkpoint-")
	if !ok {
		return 0, false
	}
	num, ok = strings.CutSuffix(num, ".json")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// Genesis is the value checkpoint 1 links to, so a chain that was started
// over, or copied from another tenant, does not verify.
func Genesis(tenant string) string {
	sum := sha256.Sum256([]byte("streamuploader-drive-audit-genesis\n" + tenant + "\n"))
	return hex.EncodeToString(sum[:])
}

// MerkleRoot hashes the leaves pairwise up to one root. An odd leaf is
// paired with itself; the root of a single leaf is that leaf's hash.
func MerkleRoot(leaves []Leaf) string {
	if len(leaves) == 0 {
		return ""
	}
	level := make([][]byte, 0, len(leaves))
	for _, l := range leaves {
		b, err := hex.DecodeString(l.SHA256)
		if err != nil || len(b) != sha256.Size {
			h := sha256.Sum256([]byte(l.SHA256))
			b = h[:]
		}
		level = append(level, b)
	}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			right := level[i]
			if i+1 < len(level) {
				right = level[i+1]
			}
			h := sha256.Sum256(append(append([]byte{}, level[i]...), right...))
			next = append(next, h[:])
		}
		level = next
	}
	return hex.EncodeToString(level[0])
}

// Build makes checkpoint n from a batch of entries that sort after the
// previous checkpoint. Entries must be non-empty and in key order.
func Build(tenant string, n int64, prevSHA256 string, entries []journal.Entry, at time.Time) *Checkpoint {
	cp := &Checkpoint{
		N:                    n,
		TenantID:             tenant,
		At:                   at.UTC(),
		PrevCheckpointSHA256: prevSHA256,
		JournalRange:         Range{First: entries[0].Key, Last: entries[len(entries)-1].Key},
		EventCount:           len(entries),
		Leaves:               make([]Leaf, 0, len(entries)),
	}
	seen := map[string]bool{}
	for _, e := range entries {
		cp.Leaves = append(cp.Leaves, Leaf{Key: e.Key, SHA256: e.Digest})
		ev := e.Event
		if ev == nil || ev.State == nil || (ev.Type != journal.FileCreated && ev.Type != journal.FileVersioned) {
			continue
		}
		st := ev.State
		if st.ObjectKey == "" || seen[st.ObjectKey] {
			continue
		}
		seen[st.ObjectKey] = true
		cp.Originals = append(cp.Originals, Original{FileID: st.FileID, ObjectKey: st.ObjectKey, ChecksumSHA256: st.ChecksumSHA256, SizeBytes: st.SizeBytes})
	}
	cp.JournalMerkleRoot = MerkleRoot(cp.Leaves)
	return cp
}

// Head is where the chain currently ends.
type Head struct {
	// N is the newest checkpoint number; zero when none exists.
	N int64
	// SHA256 of the newest checkpoint object, or the genesis value.
	SHA256 string
	// LastKey is the newest journal key covered.
	LastKey string
}

// Chain reads and extends the checkpoints of one tenant.
type Chain struct {
	Objects storage.Store
	Bucket  string
	Prefix  string
	Tenant  string
	// Lock is the object lock attached to every checkpoint written.
	Lock storage.LockPolicy
}

// Keys lists the checkpoint keys in sequence order.
func (c *Chain) Keys(ctx context.Context) ([]string, error) {
	list, err := c.Objects.ListObjects(ctx, storage.ListInput{Bucket: c.Bucket, Prefix: KeyPrefix(c.Prefix, c.Tenant)})
	if err != nil {
		return nil, fmt.Errorf("audit: list checkpoints: %w", err)
	}
	keys := make([]string, 0, len(list.Keys))
	for _, k := range list.Keys {
		if _, ok := ParseN(k); ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := ParseN(keys[i])
		b, _ := ParseN(keys[j])
		return a < b
	})
	return keys, nil
}

// Head finds the newest checkpoint; without one the head is the genesis.
func (c *Chain) Head(ctx context.Context) (Head, error) {
	keys, err := c.Keys(ctx)
	if err != nil {
		return Head{}, err
	}
	if len(keys) == 0 {
		return Head{SHA256: Genesis(c.Tenant)}, nil
	}
	cp, raw, err := c.ReadKey(ctx, keys[len(keys)-1])
	if err != nil {
		return Head{}, err
	}
	return Head{N: cp.N, SHA256: digest(raw), LastKey: cp.JournalRange.Last}, nil
}

// Read fetches checkpoint n.
func (c *Chain) Read(ctx context.Context, n int64) (*Checkpoint, []byte, error) {
	return c.ReadKey(ctx, Key(c.Prefix, c.Tenant, n))
}

// ReadKey fetches a checkpoint by key and returns its stored bytes too.
func (c *Chain) ReadKey(ctx context.Context, key string) (*Checkpoint, []byte, error) {
	out, err := c.Objects.GetObject(ctx, storage.GetInput{Bucket: c.Bucket, Key: key})
	if err != nil {
		return nil, nil, fmt.Errorf("audit: get %s: %w", key, err)
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(out.Body, 64<<20))
	if err != nil {
		return nil, nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, nil, fmt.Errorf("audit: parse %s: %w", key, err)
	}
	return &cp, raw, nil
}

// Append writes the next checkpoint after head covering entries, and returns
// the new head. Entries must sort after head.LastKey.
func (c *Chain) Append(ctx context.Context, head Head, entries []journal.Entry, at time.Time) (Head, *Checkpoint, error) {
	if len(entries) == 0 {
		return head, nil, fmt.Errorf("audit: nothing to checkpoint")
	}
	if entries[0].Key <= head.LastKey {
		return head, nil, fmt.Errorf("audit: entries start at %s, at or before the chain head %s", entries[0].Key, head.LastKey)
	}
	prev := head.SHA256
	if prev == "" {
		prev = Genesis(c.Tenant)
	}
	cp := Build(c.Tenant, head.N+1, prev, entries, at)
	raw, err := json.Marshal(cp)
	if err != nil {
		return head, nil, err
	}
	key := Key(c.Prefix, c.Tenant, cp.N)
	if _, err := c.Objects.PutObject(ctx, storage.PutInput{
		Bucket:      c.Bucket,
		Key:         key,
		Body:        bytes.NewReader(raw),
		ContentType: "application/json",
		Retention:   c.Lock.For(at),
	}); err != nil {
		return head, nil, fmt.Errorf("audit: put %s: %w", key, err)
	}
	return Head{N: cp.N, SHA256: digest(raw), LastKey: cp.JournalRange.Last}, cp, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
