// Package sidecar talks to the drivesearch process (search/ in this
// repository): a tantivy index behind a JSON-lines protocol on stdin/stdout.
// See .knowledge/concepts/system/search-sidecar.yaml.
package sidecar

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Client runs one sidecar process for one index directory. Calls are
// serialized; the sidecar handles one request at a time.
type Client struct {
	Binary    string
	IndexDir  string
	Tokenizer string
	Logger    *slog.Logger
	// StartTimeout bounds the wait for the ready line.
	StartTimeout time.Duration

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr io.ReadCloser
	nextID atomic.Int64
	closed bool
}

// ErrSchemaMismatch is reported when the index directory was built by a
// sidecar with another schema; the caller should wipe the directory and rebuild.
var ErrSchemaMismatch = errors.New("sidecar: index schema mismatch")

// New returns a client; Start launches the process.
func New(binary, indexDir string) *Client {
	return &Client{Binary: binary, IndexDir: indexDir, Tokenizer: "lindera", StartTimeout: 60 * time.Second}
}

// FindBinary locates drivesearch: an explicit path, then next to the running
// executable, then PATH.
func FindBinary(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "drivesearch")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("drivesearch"); err == nil {
		return p, nil
	}
	return "", errors.New("sidecar: drivesearch binary not found; set DRIVE_SEARCH_BIN")
}

func (c *Client) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Start launches the process and waits for its ready line.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startLocked(ctx)
}

func (c *Client) startLocked(ctx context.Context) error {
	if c.cmd != nil {
		return nil
	}
	if err := os.MkdirAll(c.IndexDir, 0o755); err != nil {
		return err
	}
	args := []string{"--index-dir", c.IndexDir}
	if c.Tokenizer != "" {
		args = append(args, "--tokenizer", c.Tokenizer)
	}
	cmd := exec.Command(c.Binary, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sidecar: start %s: %w", c.Binary, err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			c.logger().Warn("drivesearch_stderr", "line", sc.Text())
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	c.cmd, c.stdin, c.stdout, c.stderr = cmd, stdin, scanner, stderr

	timeout := c.StartTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	readyCh := make(chan error, 1)
	go func() {
		var line rawResponse
		if !scanner.Scan() {
			readyCh <- fmt.Errorf("sidecar: exited before ready: %v", scanner.Err())
			return
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			readyCh <- fmt.Errorf("sidecar: bad ready line %q: %w", scanner.Text(), err)
			return
		}
		if !line.OK {
			if isSchemaMismatch(line.Error) {
				readyCh <- fmt.Errorf("%w: %s", ErrSchemaMismatch, line.Error)
				return
			}
			readyCh <- fmt.Errorf("sidecar: %s", line.Error)
			return
		}
		readyCh <- nil
	}()
	select {
	case err := <-readyCh:
		if err != nil {
			c.killLocked()
			return err
		}
	case <-time.After(timeout):
		c.killLocked()
		return errors.New("sidecar: timeout waiting for ready")
	case <-ctx.Done():
		c.killLocked()
		return ctx.Err()
	}
	c.logger().Info("drivesearch_started", "binary", c.Binary, "index_dir", c.IndexDir, "tokenizer", c.Tokenizer)
	return nil
}

func isSchemaMismatch(msg string) bool {
	return len(msg) >= len("schema_mismatch") && msg[:len("schema_mismatch")] == "schema_mismatch"
}

func (c *Client) killLocked() {
	if c.cmd == nil {
		return
	}
	_ = c.stdin.Close()
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	c.cmd, c.stdin, c.stdout = nil, nil, nil
}

// Close stops the process.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.cmd == nil {
		return nil
	}
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
	c.cmd, c.stdin, c.stdout = nil, nil, nil
	return nil
}

// Reset stops the process and deletes the index directory, for a rebuild.
func (c *Client) Reset(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killLocked()
	if err := os.RemoveAll(c.IndexDir); err != nil {
		return err
	}
	return c.startLocked(ctx)
}

type rawResponse struct {
	ID    json.RawMessage `json:"id"`
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
}

// call sends one request and decodes the response into out. A broken pipe
// restarts the process once and returns the error; the caller retries.
func (c *Client) call(ctx context.Context, op string, req map[string]any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("sidecar: closed")
	}
	if c.cmd == nil {
		if err := c.startLocked(ctx); err != nil {
			return err
		}
	}
	if req == nil {
		req = map[string]any{}
	}
	id := c.nextID.Add(1)
	req["id"] = id
	req["op"] = op
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := c.stdin.Write(line); err != nil {
		c.logger().Error("drivesearch_write_failed", "error", err)
		c.killLocked()
		return fmt.Errorf("sidecar: write: %w", err)
	}
	if !c.stdout.Scan() {
		err := c.stdout.Err()
		c.killLocked()
		return fmt.Errorf("sidecar: process ended: %v", err)
	}
	raw := append([]byte(nil), c.stdout.Bytes()...)
	var head rawResponse
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("sidecar: bad response: %w", err)
	}
	if !head.OK {
		return fmt.Errorf("sidecar: %s: %s", op, head.Error)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("sidecar: decode %s: %w", op, err)
		}
	}
	return nil
}

// Doc is one index document: a file or one page of a file.
type Doc struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Tenant   string   `json:"tenant"`
	FileID   string   `json:"file_id"`
	Name     string   `json:"name,omitempty"`
	Ext      string   `json:"ext,omitempty"`
	Size     *int64   `json:"size,omitempty"`
	Created  *int64   `json:"created,omitempty"`
	Modified *int64   `json:"modified,omitempty"`
	Uploaded *int64   `json:"uploaded,omitempty"`
	Author   string   `json:"author,omitempty"`
	Facets   []string `json:"facets,omitempty"`
	Lat      *float64 `json:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty"`
	Body     string   `json:"body,omitempty"`
	Page     *int64   `json:"page,omitempty"`
	View     string   `json:"view,omitempty"`
}

func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, "ping", nil, nil)
}

// Upsert replaces the documents of the files contained in docs.
func (c *Client) Upsert(ctx context.Context, docs []Doc) error {
	if len(docs) == 0 {
		return nil
	}
	return c.call(ctx, "upsert", map[string]any{"docs": docs}, nil)
}

func (c *Client) Delete(ctx context.Context, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return nil
	}
	return c.call(ctx, "delete", map[string]any{"file_ids": fileIDs}, nil)
}

// Commit makes pending changes searchable and returns the document count.
func (c *Client) Commit(ctx context.Context) (int64, error) {
	var out struct {
		NumDocs int64 `json:"num_docs"`
	}
	if err := c.call(ctx, "commit", nil, &out); err != nil {
		return 0, err
	}
	return out.NumDocs, nil
}

type Stats struct {
	NumDocs       int64  `json:"num_docs"`
	Segments      int    `json:"segments"`
	SchemaVersion string `json:"schema_version"`
}

func (c *Client) Stats(ctx context.Context) (Stats, error) {
	var out Stats
	err := c.call(ctx, "stats", nil, &out)
	return out, err
}

// SearchRequest selects file documents.
type SearchRequest struct {
	Tenant string
	Query  string
	// Facets must all be present on the file. With Exact they must match a
	// path exactly (the folder itself); otherwise the path or a descendant.
	Facets []string
	Exact  bool
	// Sort is score, name, modified, created, uploaded or size, with a
	// leading "-" for descending. Empty picks score for a query and name
	// otherwise.
	Sort      string
	Limit     int
	Offset    int
	WithPages bool
}

type Hit struct {
	FileID  string  `json:"file_id"`
	Score   float64 `json:"score"`
	Name    string  `json:"name"`
	Page    int64   `json:"page,omitempty"`
	View    string  `json:"view,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
}

type SearchResponse struct {
	Total int   `json:"total"`
	Hits  []Hit `json:"hits"`
}

func (c *Client) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	m := map[string]any{
		"q":           req.Query,
		"tenant":      req.Tenant,
		"facets_any":  nonNil(req.Facets),
		"facet_exact": req.Exact,
		"limit":       req.Limit,
		"offset":      req.Offset,
		"with_pages":  req.WithPages,
	}
	if req.Limit <= 0 {
		m["limit"] = 50
	}
	if req.Sort != "" {
		m["sort"] = req.Sort
	}
	var out SearchResponse
	err := c.call(ctx, "search", m, &out)
	return out, err
}

type FacetsRequest struct {
	Tenant string
	Path   string
	Query  string
	Facets []string
	Exact  bool
	Limit  int
}

type FacetChild struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

type FacetsResponse struct {
	Path     string       `json:"path"`
	Children []FacetChild `json:"children"`
}

func (c *Client) Facets(ctx context.Context, req FacetsRequest) (FacetsResponse, error) {
	m := map[string]any{
		"tenant":      req.Tenant,
		"path":        req.Path,
		"q":           req.Query,
		"facets_any":  nonNil(req.Facets),
		"facet_exact": req.Exact,
	}
	if req.Limit > 0 {
		m["limit"] = req.Limit
	}
	var out FacetsResponse
	err := c.call(ctx, "facets", m, &out)
	return out, err
}

type BestPage struct {
	Found   bool   `json:"found"`
	Page    int64  `json:"page"`
	View    string `json:"view"`
	Snippet string `json:"snippet"`
}

func (c *Client) BestPage(ctx context.Context, tenant, fileID, query string) (BestPage, error) {
	var out BestPage
	err := c.call(ctx, "best_page", map[string]any{"tenant": tenant, "file_id": fileID, "q": query}, &out)
	return out, err
}

// nonNil keeps JSON arrays as [] rather than null, which the sidecar rejects.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
