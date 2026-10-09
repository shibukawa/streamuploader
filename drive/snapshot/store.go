package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"streamuploader/drive/objerr"
	"streamuploader/internal/storage"
)

// ErrLocalFileMissing is returned by Publish when a file the sidecar listed
// is not in the index directory; the caller asks the sidecar again.
var ErrLocalFileMissing = errors.New("snapshot: local index file missing")

// Store reads and writes one tenant's snapshot in the bucket.
type Store struct {
	Objects storage.Store
	Bucket  string
	// Prefix is the root of all Drive keys, such as "drive/".
	Prefix string
	Tenant string
	Logger *slog.Logger
	// Now is replaceable for tests.
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// KeyPrefix is the tenant's snapshot prefix in the bucket.
func (s *Store) KeyPrefix() string {
	return s.Prefix + "search/" + s.Tenant + "/"
}

// PointerKey is the pointer's object key.
func (s *Store) PointerKey() string {
	return s.KeyPrefix() + PointerName
}

// FileKey is a segment file's object key.
func (s *Store) FileKey(name string) string {
	return s.KeyPrefix() + name
}

// HeadPointer returns the pointer's ETag, or found=false when none is published.
func (s *Store) HeadPointer(ctx context.Context) (etag string, found bool, err error) {
	head, err := s.Objects.HeadObject(ctx, storage.HeadInput{Bucket: s.Bucket, Key: s.PointerKey()})
	if err != nil {
		if objerr.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("snapshot: head pointer: %w", err)
	}
	return head.ETag, true, nil
}

// ReadPointer fetches and parses the pointer.
func (s *Store) ReadPointer(ctx context.Context) (p *Pointer, etag string, found bool, err error) {
	out, err := s.Objects.GetObject(ctx, storage.GetInput{Bucket: s.Bucket, Key: s.PointerKey()})
	if err != nil {
		if objerr.IsNotFound(err) {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("snapshot: get pointer: %w", err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, 64<<20))
	if err != nil {
		return nil, "", false, err
	}
	p, err = Decode(body)
	if err != nil {
		return nil, "", false, err
	}
	return p, out.ETag, true, nil
}

// Publication is what the indexer publishes after a commit.
type Publication struct {
	// MetaJSON and Files come from the sidecar's commit or files call.
	MetaJSON string
	Files    []string
	Opstamp  uint64
	NumDocs  int64

	SchemaVersion  string
	Tokenizer      string
	LastJournalKey string
	PendingText    map[string]string
	// Changed lists the file ids this generation re-indexed; Full marks a
	// rebuild where every file changed.
	Changed []string
	Full    bool
}

// Publish uploads the segment files of pub that prev does not already
// reference, then writes the pointer as generation prev+1. Files prev
// referenced and pub does not are retired; retired files older than grace
// are deleted before the new pointer is written, so a reader of the previous
// pointer has had the whole grace period to fetch them.
func (s *Store) Publish(ctx context.Context, pub Publication, prev *Pointer, dir string, grace time.Duration) (*Pointer, string, error) {
	now := s.now()
	have := map[string]FileRef{}
	var prevHistory []Change
	var prevRetired []Retired
	gen := int64(1)
	if prev != nil {
		have = prev.FileSet()
		prevHistory = prev.History
		prevRetired = prev.Retired
		gen = prev.Generation + 1
	}
	files := make([]FileRef, 0, len(pub.Files))
	current := map[string]bool{}
	uploaded := 0
	for _, name := range pub.Files {
		if !IsSegmentFile(name) {
			return nil, "", fmt.Errorf("snapshot: refusing to publish non-segment file %q", name)
		}
		path := filepath.Join(dir, name)
		st, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, "", fmt.Errorf("%w: %s", ErrLocalFileMissing, name)
			}
			return nil, "", err
		}
		ref := FileRef{Name: name, Size: st.Size()}
		current[name] = true
		if h, ok := have[name]; ok && h.Size == ref.Size {
			files = append(files, ref)
			continue
		}
		if err := s.upload(ctx, path, name); err != nil {
			return nil, "", err
		}
		uploaded++
		files = append(files, ref)
	}
	// Retire what the previous pointer referenced and this one does not;
	// delete what has been retired for longer than the grace period.
	var retired []Retired
	deleted := 0
	for _, r := range prevRetired {
		if current[r.Name] {
			continue
		}
		if now.Sub(r.At) >= grace {
			if err := s.Objects.DeleteObject(ctx, storage.DeleteInput{Bucket: s.Bucket, Key: s.FileKey(r.Name)}); err != nil {
				s.logger().Warn("index_snapshot_delete_failed", "name", r.Name, "error", err)
				retired = append(retired, r)
				continue
			}
			deleted++
			continue
		}
		retired = append(retired, r)
	}
	for _, f := range files {
		delete(have, f.Name)
	}
	retiredNames := make([]string, 0, len(have))
	for name := range have {
		retiredNames = append(retiredNames, name)
	}
	sort.Strings(retiredNames)
	for _, name := range retiredNames {
		retired = append(retired, Retired{Name: name, At: now})
	}
	pending := make(map[string]string, len(pub.PendingText))
	for k, v := range pub.PendingText {
		pending[k] = v
	}
	changed := append([]string(nil), pub.Changed...)
	sort.Strings(changed)
	changed = slices.Compact(changed)
	p := &Pointer{
		SchemaVersion:  pub.SchemaVersion,
		Tokenizer:      pub.Tokenizer,
		Generation:     gen,
		Opstamp:        pub.Opstamp,
		PublishedAt:    now,
		LastJournalKey: pub.LastJournalKey,
		PendingText:    pending,
		NumDocs:        pub.NumDocs,
		Files:          files,
		Retired:        retired,
		History:        appendHistory(prevHistory, Change{Generation: gen, Full: pub.Full, Changed: changed}),
		Meta:           []byte(pub.MetaJSON),
	}
	body, err := Encode(p)
	if err != nil {
		return nil, "", err
	}
	res, err := s.Objects.PutObject(ctx, storage.PutInput{
		Bucket:      s.Bucket,
		Key:         s.PointerKey(),
		Body:        strings.NewReader(string(body)),
		ContentType: "application/json",
	})
	if err != nil {
		return nil, "", fmt.Errorf("snapshot: put pointer: %w", err)
	}
	etag := res.ETag
	if etag == "" {
		if head, found, err := s.HeadPointer(ctx); err == nil && found {
			etag = head
		}
	}
	s.logger().Info("index_snapshot_published", "tenant", s.Tenant, "generation", gen, "files", len(files), "uploaded", uploaded, "retired", len(retired), "deleted", deleted, "num_docs", pub.NumDocs)
	return p, etag, nil
}

func (s *Store) upload(ctx context.Context, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := s.Objects.PutObject(ctx, storage.PutInput{
		Bucket:      s.Bucket,
		Key:         s.FileKey(name),
		Body:        f,
		ContentType: "application/octet-stream",
	}); err != nil {
		return fmt.Errorf("snapshot: put %s: %w", name, err)
	}
	return nil
}

// SweepOrphans deletes segment files under the tenant prefix that the
// current pointer neither references nor retires and that are older than
// grace: leftovers of a publish that crashed before writing its pointer.
func (s *Store) SweepOrphans(ctx context.Context, p *Pointer, grace time.Duration) (int, error) {
	list, err := s.Objects.ListObjects(ctx, storage.ListInput{Bucket: s.Bucket, Prefix: s.KeyPrefix()})
	if err != nil {
		return 0, fmt.Errorf("snapshot: list: %w", err)
	}
	known := map[string]bool{PointerName: true}
	for _, f := range p.Files {
		known[f.Name] = true
	}
	for _, r := range p.Retired {
		known[r.Name] = true
	}
	now := s.now()
	deleted := 0
	for _, key := range list.Keys {
		name := strings.TrimPrefix(key, s.KeyPrefix())
		if known[name] || !IsSegmentFile(name) {
			continue
		}
		head, err := s.Objects.HeadObject(ctx, storage.HeadInput{Bucket: s.Bucket, Key: key})
		if err != nil {
			if objerr.IsNotFound(err) {
				continue
			}
			return deleted, fmt.Errorf("snapshot: head %s: %w", name, err)
		}
		if now.Sub(head.LastModified) < grace {
			continue
		}
		if err := s.Objects.DeleteObject(ctx, storage.DeleteInput{Bucket: s.Bucket, Key: key}); err != nil {
			return deleted, fmt.Errorf("snapshot: delete orphan %s: %w", name, err)
		}
		deleted++
	}
	return deleted, nil
}

// Download fetches the files of p that are missing from dir (or differ in
// size) and then writes tantivy's meta.json. Returns the number fetched.
func (s *Store) Download(ctx context.Context, p *Pointer, dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	fetched := 0
	for _, f := range p.Files {
		if !IsSegmentFile(f.Name) {
			return fetched, fmt.Errorf("snapshot: refusing to download non-segment file %q", f.Name)
		}
		path := filepath.Join(dir, f.Name)
		if st, err := os.Stat(path); err == nil && st.Size() == f.Size {
			continue
		}
		if err := s.download(ctx, f, path); err != nil {
			return fetched, err
		}
		fetched++
	}
	if err := WriteMeta(dir, p.Meta); err != nil {
		return fetched, err
	}
	return fetched, nil
}

func (s *Store) download(ctx context.Context, f FileRef, path string) error {
	out, err := s.Objects.GetObject(ctx, storage.GetInput{Bucket: s.Bucket, Key: s.FileKey(f.Name)})
	if err != nil {
		return fmt.Errorf("snapshot: get %s: %w", f.Name, err)
	}
	defer out.Body.Close()
	tmp := filepath.Join(filepath.Dir(path), ".download-"+f.Name+".tmp")
	w, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, out.Body)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("snapshot: write %s: %w", f.Name, err)
	}
	if n != f.Size {
		_ = os.Remove(tmp)
		return fmt.Errorf("snapshot: %s has %d bytes, pointer says %d", f.Name, n, f.Size)
	}
	return os.Rename(tmp, path)
}

// WriteMeta writes tantivy's meta.json atomically.
func WriteMeta(dir string, meta []byte) error {
	tmp := filepath.Join(dir, ".meta.json.tmp")
	if err := os.WriteFile(tmp, meta, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "meta.json"))
}

// Prune deletes the segment files in dir that keep does not reference. The
// caller runs it only while the sidecar is idle (after a commit or reload),
// because a segment being written is not referenced yet either.
func Prune(dir string, keep []FileRef) (int, error) {
	keepSet := make(map[string]bool, len(keep))
	for _, f := range keep {
		keepSet[f.Name] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || keepSet[name] || !IsSegmentFile(name) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
