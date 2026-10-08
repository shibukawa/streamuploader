// Package memstore is an in-memory storage.Store for tests and for running the
// Drive without an object store. Keys are kept sorted so ListObjects behaves
// like S3 ListObjectsV2, including StartAfter.
package memstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"streamuploader/internal/storage"
)

// ErrNotFound is returned for a missing key. Callers that need to tell a
// missing object from other failures can use errors.Is.
var ErrNotFound = errors.New("memstore: not found")

type object struct {
	body         []byte
	contentType  string
	metadata     map[string]string
	etag         string
	lastModified time.Time
}

// Store holds objects in memory. The zero value is not usable; call New.
type Store struct {
	mu      sync.RWMutex
	objects map[string]map[string]*object // bucket -> key -> object
	// PresignBase is the URL prefix used for presigned URLs, for tests.
	PresignBase string
}

func New() *Store {
	return &Store{objects: map[string]map[string]*object{}, PresignBase: "memory://"}
}

func (s *Store) bucket(name string) map[string]*object {
	b, ok := s.objects[name]
	if !ok {
		b = map[string]*object{}
		s.objects[name] = b
	}
	return b
}

func (s *Store) PutObject(_ context.Context, input storage.PutInput) (storage.PutResult, error) {
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return storage.PutResult{}, err
	}
	sum := sha256.Sum256(body)
	obj := &object{
		body:         body,
		contentType:  input.ContentType,
		metadata:     cloneMap(input.Metadata),
		etag:         `"` + hex.EncodeToString(sum[:16]) + `"`,
		lastModified: time.Now().UTC(),
	}
	s.mu.Lock()
	s.bucket(input.Bucket)[input.Key] = obj
	s.mu.Unlock()
	return storage.PutResult{ETag: obj.etag}, nil
}

func (s *Store) CopyObject(_ context.Context, input storage.CopyInput) (storage.CopyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.bucket(input.Bucket)[input.SourceKey]
	if !ok {
		return storage.CopyResult{}, ErrNotFound
	}
	dst := *src
	dst.body = append([]byte(nil), src.body...)
	if input.ContentType != "" {
		dst.contentType = input.ContentType
	}
	if input.Metadata != nil {
		dst.metadata = cloneMap(input.Metadata)
	}
	dst.lastModified = time.Now().UTC()
	s.bucket(input.Bucket)[input.Key] = &dst
	return storage.CopyResult{ETag: dst.etag}, nil
}

func (s *Store) GetObject(_ context.Context, input storage.GetInput) (storage.GetResult, error) {
	s.mu.RLock()
	obj, ok := s.bucket(input.Bucket)[input.Key]
	s.mu.RUnlock()
	if !ok {
		return storage.GetResult{}, ErrNotFound
	}
	body := obj.body
	contentRange := ""
	if input.Range != "" {
		start, end, err := parseRange(input.Range, int64(len(obj.body)))
		if err != nil {
			return storage.GetResult{}, err
		}
		body = obj.body[start : end+1]
		contentRange = fmt.Sprintf("bytes %d-%d/%d", start, end, len(obj.body))
	}
	return storage.GetResult{
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentType:   obj.contentType,
		ContentLength: int64(len(body)),
		ContentRange:  contentRange,
		ETag:          obj.etag,
		LastModified:  obj.lastModified,
		Metadata:      cloneMap(obj.metadata),
	}, nil
}

func (s *Store) HeadObject(_ context.Context, input storage.HeadInput) (storage.HeadResult, error) {
	s.mu.RLock()
	obj, ok := s.bucket(input.Bucket)[input.Key]
	s.mu.RUnlock()
	if !ok {
		return storage.HeadResult{}, ErrNotFound
	}
	return storage.HeadResult{
		ContentType:   obj.contentType,
		ContentLength: int64(len(obj.body)),
		ETag:          obj.etag,
		LastModified:  obj.lastModified,
		Metadata:      cloneMap(obj.metadata),
	}, nil
}

func (s *Store) ListObjects(_ context.Context, input storage.ListInput) (storage.ListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var keys []string
	for key := range s.bucket(input.Bucket) {
		if !strings.HasPrefix(key, input.Prefix) {
			continue
		}
		if input.StartAfter != "" && key <= input.StartAfter {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if input.MaxKeys > 0 && len(keys) > input.MaxKeys {
		keys = keys[:input.MaxKeys]
	}
	return storage.ListResult{Keys: keys}, nil
}

func (s *Store) PresignGetObject(_ context.Context, input storage.PresignGetInput) (storage.PresignGetResult, error) {
	if input.Expires <= 0 {
		input.Expires = 15 * time.Minute
	}
	return storage.PresignGetResult{
		URL:       s.PresignBase + input.Bucket + "/" + input.Key,
		ExpiresAt: time.Now().UTC().Add(input.Expires),
	}, nil
}

func (s *Store) DeleteObject(_ context.Context, input storage.DeleteInput) error {
	s.mu.Lock()
	delete(s.bucket(input.Bucket), input.Key)
	s.mu.Unlock()
	return nil
}

// Len returns the number of objects in a bucket, for tests.
func (s *Store) Len(bucket string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.bucket(bucket))
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// parseRange handles "bytes=start-end", "bytes=start-" and "bytes=-suffix".
func parseRange(header string, size int64) (int64, int64, error) {
	spec, ok := strings.CutPrefix(header, "bytes=")
	if !ok {
		return 0, 0, fmt.Errorf("memstore: unsupported range %q", header)
	}
	startText, endText, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, fmt.Errorf("memstore: bad range %q", header)
	}
	if startText == "" {
		suffix, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, fmt.Errorf("memstore: bad range %q", header)
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, nil
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, fmt.Errorf("memstore: range start out of bounds %q", header)
	}
	end := size - 1
	if endText != "" {
		end, err = strconv.ParseInt(endText, 10, 64)
		if err != nil || end < start {
			return 0, 0, fmt.Errorf("memstore: bad range %q", header)
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, nil
}
