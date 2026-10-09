package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"

	"streamuploader/drive/journal"
	"streamuploader/drive/objerr"
	"streamuploader/internal/storage"
)

// Finding kinds reported by Verify.
const (
	FindingChainBreak       = "chain_break"
	FindingSequenceGap      = "sequence_gap"
	FindingRootMismatch     = "root_mismatch"
	FindingRangeMismatch    = "range_mismatch"
	FindingMissingEvent     = "missing_event"
	FindingExtraEvent       = "extra_event"
	FindingModifiedEvent    = "modified_event"
	FindingMissingOriginal  = "missing_original"
	FindingModifiedOriginal = "modified_original"
	FindingUnreadable       = "unreadable"
)

// Finding is one verification failure.
type Finding struct {
	Checkpoint int64  `json:"checkpoint,omitempty"`
	Kind       string `json:"kind"`
	Key        string `json:"key,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

func (f Finding) String() string {
	s := f.Kind
	if f.Checkpoint > 0 {
		s += fmt.Sprintf(" checkpoint=%d", f.Checkpoint)
	}
	if f.Key != "" {
		s += " key=" + f.Key
	}
	if f.Detail != "" {
		s += ": " + f.Detail
	}
	return s
}

// Report is the outcome of a verification run.
type Report struct {
	Tenant            string `json:"tenant"`
	Checkpoints       int    `json:"checkpoints"`
	HeadN             int64  `json:"head_n"`
	EventsVerified    int    `json:"events_verified"`
	OriginalsVerified int    `json:"originals_verified"`
	// UnverifiedTail counts journal events newer than the last checkpoint;
	// they are covered by the next fold.
	UnverifiedTail int       `json:"unverified_tail"`
	Findings       []Finding `json:"findings"`
}

// OK reports whether nothing was found.
func (r *Report) OK() bool { return len(r.Findings) == 0 }

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

// VerifyOptions tunes a run.
type VerifyOptions struct {
	// HashOriginals re-reads every registered original and compares its
	// SHA-256 with the checksum recorded at upload; without it only
	// existence and size are checked.
	HashOriginals bool
}

type coveredLeaf struct {
	Leaf
	checkpoint int64
}

// Verify walks the chain, recomputes every root, compares the covered keys
// with the journal, and checks that registered originals are still there.
func (c *Chain) Verify(ctx context.Context, js *journal.Store, opts VerifyOptions) (*Report, error) {
	rep := &Report{Tenant: c.Tenant, Findings: []Finding{}}
	keys, err := c.Keys(ctx)
	if err != nil {
		return nil, err
	}
	var covered []coveredLeaf
	var originals []Original
	prevSHA := Genesis(c.Tenant)
	var prevN int64
	prevLast := ""
	for _, key := range keys {
		cp, raw, err := c.ReadKey(ctx, key)
		if err != nil {
			rep.add(Finding{Kind: FindingUnreadable, Key: key, Detail: err.Error()})
			return rep, nil
		}
		rep.Checkpoints++
		rep.HeadN = cp.N
		if cp.N != prevN+1 {
			rep.add(Finding{Checkpoint: cp.N, Kind: FindingSequenceGap, Key: key, Detail: fmt.Sprintf("expected checkpoint %d after %d", prevN+1, prevN)})
		}
		if cp.PrevCheckpointSHA256 != prevSHA {
			rep.add(Finding{Checkpoint: cp.N, Kind: FindingChainBreak, Key: key, Detail: "prev_checkpoint_sha256 does not match the previous checkpoint"})
		}
		if cp.EventCount != len(cp.Leaves) || MerkleRoot(cp.Leaves) != cp.JournalMerkleRoot {
			rep.add(Finding{Checkpoint: cp.N, Kind: FindingRootMismatch, Key: key, Detail: "leaves do not reproduce journal_merkle_root"})
		}
		if len(cp.Leaves) == 0 || cp.Leaves[0].Key != cp.JournalRange.First || cp.Leaves[len(cp.Leaves)-1].Key != cp.JournalRange.Last {
			rep.add(Finding{Checkpoint: cp.N, Kind: FindingRangeMismatch, Key: key, Detail: "journal_range does not match the leaves"})
		}
		for i, l := range cp.Leaves {
			if l.Key <= prevLast {
				rep.add(Finding{Checkpoint: cp.N, Kind: FindingRangeMismatch, Key: l.Key, Detail: "leaf is not after the previous checkpoint"})
				continue
			}
			if i > 0 && l.Key <= cp.Leaves[i-1].Key {
				rep.add(Finding{Checkpoint: cp.N, Kind: FindingRangeMismatch, Key: l.Key, Detail: "leaves are not in key order"})
				continue
			}
			covered = append(covered, coveredLeaf{Leaf: l, checkpoint: cp.N})
			prevLast = l.Key
		}
		originals = append(originals, cp.Originals...)
		prevSHA = digest(raw)
		prevN = cp.N
	}

	// Compare the covered keys with what the journal holds now.
	journalKeys, err := js.ListKeys(ctx, c.Tenant, "", 0)
	if err != nil {
		return nil, err
	}
	sort.Strings(journalKeys)
	coveredEnd := ""
	if len(covered) > 0 {
		coveredEnd = covered[len(covered)-1].Key
	}
	i, j := 0, 0
	for i < len(covered) || j < len(journalKeys) {
		switch {
		case j >= len(journalKeys) || (i < len(covered) && covered[i].Key < journalKeys[j]):
			rep.add(Finding{Checkpoint: covered[i].checkpoint, Kind: FindingMissingEvent, Key: covered[i].Key, Detail: "journal object is gone"})
			i++
		case i >= len(covered) || journalKeys[j] < covered[i].Key:
			if journalKeys[j] > coveredEnd {
				rep.UnverifiedTail++
			} else {
				rep.add(Finding{Kind: FindingExtraEvent, Key: journalKeys[j], Detail: "journal object inside a checkpointed range is not covered by any checkpoint"})
			}
			j++
		default:
			e, err := js.Read(ctx, covered[i].Key)
			if err != nil {
				rep.add(Finding{Checkpoint: covered[i].checkpoint, Kind: FindingUnreadable, Key: covered[i].Key, Detail: err.Error()})
			} else if e.Digest != covered[i].SHA256 {
				rep.add(Finding{Checkpoint: covered[i].checkpoint, Kind: FindingModifiedEvent, Key: covered[i].Key, Detail: "stored bytes differ from the checkpointed digest"})
			} else {
				rep.EventsVerified++
			}
			i++
			j++
		}
	}

	// Originals: present, same size, and optionally the same bytes.
	for _, o := range originals {
		head, err := c.Objects.HeadObject(ctx, storage.HeadInput{Bucket: c.Bucket, Key: o.ObjectKey})
		if err != nil {
			if objerr.IsNotFound(err) {
				rep.add(Finding{Kind: FindingMissingOriginal, Key: o.ObjectKey, Detail: "file " + o.FileID + " original is gone"})
			} else {
				rep.add(Finding{Kind: FindingUnreadable, Key: o.ObjectKey, Detail: err.Error()})
			}
			continue
		}
		if o.SizeBytes > 0 && head.ContentLength != o.SizeBytes {
			rep.add(Finding{Kind: FindingModifiedOriginal, Key: o.ObjectKey, Detail: fmt.Sprintf("size %d, registered %d", head.ContentLength, o.SizeBytes)})
			continue
		}
		if opts.HashOriginals && o.ChecksumSHA256 != "" {
			sum, err := hashObject(ctx, c.Objects, c.Bucket, o.ObjectKey)
			if err != nil {
				rep.add(Finding{Kind: FindingUnreadable, Key: o.ObjectKey, Detail: err.Error()})
				continue
			}
			if sum != o.ChecksumSHA256 {
				rep.add(Finding{Kind: FindingModifiedOriginal, Key: o.ObjectKey, Detail: "sha256 differs from the checksum registered at upload"})
				continue
			}
		}
		rep.OriginalsVerified++
	}
	return rep, nil
}

func hashObject(ctx context.Context, store storage.Store, bucket, key string) (string, error) {
	out, err := store.GetObject(ctx, storage.GetInput{Bucket: bucket, Key: key})
	if err != nil {
		return "", err
	}
	defer out.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(h, out.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
