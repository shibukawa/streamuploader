package server

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/drive/meta"
	"streamuploader/drive/objerr"
	"streamuploader/internal/storage"
)

const previewContentType = "application/x-bdf"

// deliver serves the original or a derived asset. kind is content, download,
// preview or thumbnail. See decision:presigned-zero-egress-delivery.
func (s *Server) deliver(w http.ResponseWriter, r *http.Request, kind string) {
	f, ok, err := s.resolveFile(r.Context(), pw.PathValue(r, "id"))
	if err != nil {
		s.fail(w, r, "resolve_file", err)
		return
	}
	if !ok || f.Deleted {
		writeProblem(w, r, http.StatusNotFound, "not_found", "file not found")
		return
	}
	key, contentType, filename := assetFor(f, kind)
	if key == "" {
		writeProblem(w, r, http.StatusNotFound, "no_asset", "this file has no "+kind)
		return
	}
	attachment := kind == "download"
	if !s.recordAccess(w, r, f.FileID, key, accessKind(kind)) {
		return
	}
	s.serveObject(w, r, key, contentType, filename, attachment, kind != "content" && kind != "download")
}

// accessKind names a read in the journal: content is an inline view.
func accessKind(kind string) string {
	if kind == "content" {
		return "inline"
	}
	return kind
}

// deliverVersion serves an earlier object of a file (1-based n).
func (s *Server) deliverVersion(w http.ResponseWriter, r *http.Request, attachment bool) {
	f, ok, err := s.resolveFile(r.Context(), pw.PathValue(r, "id"))
	if err != nil {
		s.fail(w, r, "resolve_file", err)
		return
	}
	if !ok || f.Deleted {
		writeProblem(w, r, http.StatusNotFound, "not_found", "file not found")
		return
	}
	n, err := strconv.Atoi(pw.PathValue(r, "n"))
	if err != nil || n < 1 || n > len(f.Versions) {
		writeProblem(w, r, http.StatusNotFound, "no_version", "this file has no such version")
		return
	}
	v := f.Versions[n-1]
	name := v.Name
	if name == "" {
		name = f.Name
	}
	kind := "inline"
	if attachment {
		kind = "download"
	}
	if !s.recordAccess(w, r, f.FileID, v.ObjectKey, kind) {
		return
	}
	s.serveObject(w, r, v.ObjectKey, v.ContentType, name, attachment, false)
}

// serveObject redirects to a presigned URL or proxies the bytes.
func (s *Server) serveObject(w http.ResponseWriter, r *http.Request, key, contentType, filename string, attachment, derived bool) {
	if s.cfg.Delivery == "presigned" {
		disposition := ""
		if attachment {
			disposition = contentDisposition(filename)
		}
		out, err := s.deps.Store.PresignGetObject(r.Context(), storage.PresignGetInput{
			Bucket: s.cfg.Bucket, Key: key, Expires: s.cfg.PresignTTL, ResponseContentDisposition: disposition,
		})
		if err != nil {
			s.fail(w, r, "presign", err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		pw.Redirect(w, r, out.URL, http.StatusFound)
		return
	}
	s.proxyObject(w, r, key, contentType, filename, attachment, derived)
}

// assetFor picks the object key, content type and download name for a kind.
func assetFor(f *meta.File, kind string) (string, string, string) {
	switch kind {
	case "content", "download":
		return f.ObjectKey, f.ContentType, f.Name
	case "preview":
		return f.Derived.Preview.ObjectKey, previewContentType, strings.TrimSuffix(f.Name, "."+f.Ext()) + ".bdf"
	case "thumbnail":
		return f.Derived.Thumbnail.ObjectKey, f.Derived.Thumbnail.ContentType, "thumbnail"
	}
	return "", "", ""
}

func contentDisposition(filename string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(filename))
}

func (s *Server) proxyObject(w http.ResponseWriter, r *http.Request, key, contentType, filename string, attachment, derived bool) {
	out, err := s.deps.Store.GetObject(r.Context(), storage.GetInput{Bucket: s.cfg.Bucket, Key: key, Range: r.Header.Get("Range")})
	if err != nil {
		if objerr.IsNotFound(err) {
			// Derived assets are produced after the upload; the client retries.
			w.Header().Set("Cache-Control", "no-store")
			writeProblem(w, r, http.StatusNotFound, "asset_pending", "the asset is not available yet")
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "range") {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(-1, 10))
			writeProblem(w, r, http.StatusRequestedRangeNotSatisfiable, "bad_range", err.Error())
			return
		}
		s.fail(w, r, "get_object", err)
		return
	}
	defer out.Body.Close()
	if contentType == "" {
		contentType = out.ContentType
	}
	if contentType == "" {
		contentType = mime.TypeByExtension("." + strings.ToLower(strings.TrimPrefix(filename, ".")))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	pw.SetRoute(w, r)
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Content-Type-Options", "nosniff")
	if out.ETag != "" {
		h.Set("ETag", out.ETag)
	}
	if !out.LastModified.IsZero() {
		h.Set("Last-Modified", out.LastModified.UTC().Format(http.TimeFormat))
	}
	if derived {
		h.Set("Cache-Control", "private, max-age=3600")
	} else {
		h.Set("Cache-Control", "private, max-age=0, must-revalidate")
	}
	if attachment {
		h.Set("Content-Disposition", contentDisposition(filename))
	} else {
		h.Set("Content-Disposition", "inline")
	}
	if out.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(out.ContentLength, 10))
	}
	if out.ContentRange != "" {
		h.Set("Content-Range", out.ContentRange)
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, out.Body)
}
