package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"streamuploader/drive/journal"
)

// accessLog remembers recent reads so a viewer's burst of range requests on
// one object becomes one file.accessed event.
type accessLog struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// shouldRecord reports whether a read of (kind, key) by client is new within
// the window, and remembers it.
func (a *accessLog) shouldRecord(key string, window time.Duration, now time.Time) bool {
	if window <= 0 {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if last, ok := a.seen[key]; ok && now.Sub(last) < window {
		return false
	}
	if len(a.seen) > 10000 {
		for k, t := range a.seen {
			if now.Sub(t) >= window {
				delete(a.seen, k)
			}
		}
	}
	a.seen[key] = now
	return true
}

// clientHash identifies the caller without storing its address: the
// SHA-256 of the client IP, truncated. Deterministic so an auditor can
// correlate events of one client.
func clientHash(r *http.Request) string {
	ip := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if ip != "" {
		if i := strings.IndexByte(ip, ','); i >= 0 {
			ip = strings.TrimSpace(ip[:i])
		}
	} else {
		ip = r.RemoteAddr
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:8])
}

// recordAccess writes the file.accessed event that WORM mode requires
// before any bytes (or a presigned URL) leave the server. It returns false
// after writing an error response. HEAD requests are not reads.
func (s *Server) recordAccess(w http.ResponseWriter, r *http.Request, fileID, objectKey, kind string) bool {
	if !s.cfg.WORM.Enabled() || r.Method == http.MethodHead {
		return true
	}
	client := clientHash(r)
	now := time.Now().UTC()
	if !s.access.shouldRecord(s.cfg.Actor+"|"+client+"|"+kind+"|"+objectKey, s.cfg.AccessWindow, now) {
		return true
	}
	if _, err := s.deps.Journal.Append(r.Context(), &journal.Event{
		TenantID: s.cfg.Tenant,
		Type:     journal.FileAccessed,
		Actor:    s.cfg.Actor,
		At:       now,
		FileID:   fileID,
		Access:   &journal.Access{ObjectKey: objectKey, Kind: kind, ClientHash: client},
	}); err != nil {
		s.fail(w, r, "record_access", err)
		return false
	}
	if s.deps.Indexer != nil {
		s.deps.Indexer.Poke()
	}
	return true
}
