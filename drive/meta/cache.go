package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"streamuploader/internal/storage"
)

// Cache is the in-memory map of folded file states. The indexer fills it; the
// server reads it and patches results with the journal overlay.
type Cache struct {
	mu    sync.RWMutex
	files map[string]*File
}

func NewCache() *Cache {
	return &Cache{files: map[string]*File{}}
}

func (c *Cache) Get(fileID string) (*File, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f, ok := c.files[fileID]
	return f, ok
}

func (c *Cache) Put(f *File) {
	c.mu.Lock()
	c.files[f.FileID] = f
	c.mu.Unlock()
}

func (c *Cache) Delete(fileID string) {
	c.mu.Lock()
	delete(c.files, fileID)
	c.mu.Unlock()
}

func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.files)
}

// All returns the files sorted by id.
func (c *Cache) All() []*File {
	c.mu.RLock()
	out := make([]*File, 0, len(c.files))
	for _, f := range c.files {
		out = append(out, f)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].FileID < out[j].FileID })
	return out
}

// LoadSnapshots reads every meta/{tenant}/*.json object into the cache.
func (c *Cache) LoadSnapshots(ctx context.Context, store storage.Store, bucket, prefix, tenant string) (int, error) {
	list, err := store.ListObjects(ctx, storage.ListInput{Bucket: bucket, Prefix: SnapshotPrefix(prefix, tenant)})
	if err != nil {
		return 0, fmt.Errorf("list snapshots: %w", err)
	}
	const workers = 8
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	loaded := 0
	var firstErr error
	for _, key := range list.Keys {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(key string) {
			defer wg.Done()
			defer func() { <-sem }()
			f, err := ReadSnapshot(ctx, store, bucket, key)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("read %s: %w", key, err)
				}
				return
			}
			c.Put(f)
			loaded++
		}(key)
	}
	wg.Wait()
	return loaded, firstErr
}

// ReadSnapshot parses one meta object.
func ReadSnapshot(ctx context.Context, store storage.Store, bucket, key string) (*File, error) {
	out, err := store.GetObject(ctx, storage.GetInput{Bucket: bucket, Key: key})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// WriteSnapshot stores the latest state of a file.
func WriteSnapshot(ctx context.Context, store storage.Store, bucket, prefix string, f *File) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = store.PutObject(ctx, storage.PutInput{
		Bucket:      bucket,
		Key:         SnapshotKey(prefix, f.TenantID, f.FileID),
		Body:        bytesReader(body),
		ContentType: "application/json",
	})
	return err
}
