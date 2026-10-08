package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"streamuploader/drive/journal"
	"streamuploader/drive/meta"
	"streamuploader/drive/objerr"
	"streamuploader/drive/sidecar"
	"streamuploader/drive/worm"
	"streamuploader/internal/model"
	"streamuploader/internal/storage"
)

const maxBodyBytes = 1 << 20

type locationInput struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type registerRequest struct {
	Upload   model.UploadItem `json:"upload"`
	Name     string           `json:"name"`
	Tags     []string         `json:"tags"`
	Author   string           `json:"author"`
	Location *locationInput   `json:"location"`
}

// fileView is the API shape of a file: the state plus the URLs a client needs.
type fileView struct {
	*meta.File
	Facets []string `json:"facets"`
	URLs   fileURLs `json:"urls"`
}

type fileURLs struct {
	Content   string `json:"content"`
	Download  string `json:"download"`
	Preview   string `json:"preview,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

func (s *Server) view(f *meta.File) fileView {
	base := "/api/drive/files/" + f.FileID
	v := fileView{File: f, Facets: sidecar.FacetPaths(f), URLs: fileURLs{Content: base + "/content", Download: base + "/download"}}
	if f.Derived.Preview.ObjectKey != "" {
		v.URLs.Preview = base + "/preview"
	}
	if f.Derived.Thumbnail.ObjectKey != "" {
		v.URLs.Thumbnail = base + "/thumbnail"
	}
	return v
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func (s *Server) registerFile(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	up := req.Upload
	if strings.TrimSpace(up.ObjectKey) == "" {
		writeError(w, http.StatusBadRequest, "missing_object_key", "upload.object_key is required")
		return
	}
	if up.Status != "" && up.Status != model.UploadUploaded {
		writeError(w, http.StatusConflict, "upload_not_complete", fmt.Sprintf("upload status is %q", up.Status))
		return
	}
	head, err := s.deps.Store.HeadObject(r.Context(), storage.HeadInput{Bucket: s.cfg.Bucket, Key: up.ObjectKey})
	if err != nil {
		if objerr.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "object_not_found", "the uploaded object does not exist")
			return
		}
		s.fail(w, "head_object", err)
		return
	}
	tags, err := meta.NormalizeTags(req.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_tag", err.Error())
		return
	}
	now := time.Now().UTC()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = up.OriginalName
	}
	if name == "" {
		name = path.Base(up.ObjectKey)
	}
	contentType := up.ContentType
	if contentType == "" {
		contentType = head.ContentType
	}
	f := &meta.File{
		TenantID:       s.cfg.Tenant,
		FileID:         journal.NewULID(now),
		Revision:       1,
		Name:           name,
		ContentType:    contentType,
		SizeBytes:      head.ContentLength,
		ChecksumSHA256: up.ChecksumSHA256,
		ObjectKey:      up.ObjectKey,
		DisplayKey:     up.DisplayKey,
		UploadKey:      up.UploadKey,
		Tags:           tags,
		Author:         meta.Author{Override: strings.TrimSpace(req.Author)},
		Protected:      up.Protected,
		Dates:          meta.Dates{Uploaded: now, Modified: now},
	}
	if up.UploadedAt != nil && !up.UploadedAt.IsZero() {
		f.Dates.Uploaded = up.UploadedAt.UTC()
	}
	if up.Thumbnail != nil {
		f.Derived.Thumbnail = meta.Asset{ObjectKey: up.Thumbnail.ObjectKey, ContentType: up.Thumbnail.ContentType, Status: up.Thumbnail.Status}
	}
	if up.ExtractedContent != nil {
		f.Derived.Text = meta.TextAsset{ObjectKey: up.ExtractedContent.ObjectKey, Status: up.ExtractedContent.Status}
	}
	if up.Preview != nil {
		f.Derived.Preview = meta.Asset{ObjectKey: up.Preview.ObjectKey, ContentType: up.Preview.ContentType, Status: up.Preview.Status}
	}
	if req.Location != nil {
		f.Location = &meta.Location{Lat: req.Location.Lat, Lon: req.Location.Lon, Source: "user", Geohash: sidecar.Geohash(req.Location.Lat, req.Location.Lon, 8)}
	}
	facts, _ := json.Marshal(up)
	if _, err := s.appendEvent(r.Context(), &journal.Event{
		TenantID:    s.cfg.Tenant,
		Type:        journal.FileCreated,
		Actor:       s.cfg.Actor,
		FileID:      f.FileID,
		State:       f,
		UploadFacts: facts,
	}); err != nil {
		s.fail(w, "append_event", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.view(f))
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	f, ok, err := s.resolveFile(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "resolve_file", err)
		return
	}
	if !ok || f.Deleted {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	writeJSON(w, http.StatusOK, s.view(f))
}

type patchRequest struct {
	Name             *string        `json:"name"`
	Tags             *[]string      `json:"tags"`
	Author           *string        `json:"author"`
	Location         *locationInput `json:"location"`
	ClearLocation    bool           `json:"clear_location"`
	ExpectedRevision int64          `json:"expected_revision"`
}

func (s *Server) patchFile(w http.ResponseWriter, r *http.Request) {
	var req patchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cur, ok, err := s.resolveFile(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "resolve_file", err)
		return
	}
	if !ok || cur.Deleted {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != cur.Revision {
		writeError(w, http.StatusConflict, "revision_conflict", fmt.Sprintf("file is at revision %d", cur.Revision))
		return
	}
	if s.cfg.WORM == worm.Strict {
		writeReadonly(w, s.cfg.WORM.CheckUpdate(cur, cur))
		return
	}
	f := cur.Clone()
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "invalid_name", "name must not be empty")
			return
		}
		f.Name = name
	}
	if req.Tags != nil {
		tags, err := meta.NormalizeTags(*req.Tags)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_tag", err.Error())
			return
		}
		f.Tags = tags
	}
	if req.Author != nil {
		f.Author.Override = strings.TrimSpace(*req.Author)
	}
	if req.ClearLocation {
		f.Location = nil
	} else if req.Location != nil {
		f.Location = &meta.Location{Lat: req.Location.Lat, Lon: req.Location.Lon, Source: "user", Geohash: sidecar.Geohash(req.Location.Lat, req.Location.Lon, 8)}
	}
	if err := s.cfg.WORM.CheckUpdate(cur, f); err != nil {
		writeReadonly(w, err)
		return
	}
	f.Revision++
	f.Dates.Modified = time.Now().UTC()
	if _, err := s.appendEvent(r.Context(), &journal.Event{
		TenantID:         s.cfg.Tenant,
		Type:             journal.FileUpdated,
		Actor:            s.cfg.Actor,
		FileID:           f.FileID,
		State:            f,
		ExpectedRevision: req.ExpectedRevision,
	}); err != nil {
		s.fail(w, "append_event", err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(f))
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WORM.Enabled() {
		writeReadonly(w, &worm.ErrReadonly{Reason: "files cannot be deleted in WORM mode"})
		return
	}
	cur, ok, err := s.resolveFile(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "resolve_file", err)
		return
	}
	if !ok || cur.Deleted {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	f := cur.Clone()
	f.Deleted = true
	f.Revision++
	f.Dates.Modified = time.Now().UTC()
	if _, err := s.appendEvent(r.Context(), &journal.Event{
		TenantID: s.cfg.Tenant,
		Type:     journal.FileDeleted,
		Actor:    s.cfg.Actor,
		FileID:   f.FileID,
		State:    f,
	}); err != nil {
		s.fail(w, "append_event", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restoreFile undoes a delete tombstone. WORM mode has no tombstones to
// restore and refuses the call like every other state rewrite.
func (s *Server) restoreFile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WORM.Enabled() {
		writeReadonly(w, &worm.ErrReadonly{Reason: "nothing is deleted in WORM mode, so nothing can be restored"})
		return
	}
	cur, ok, err := s.resolveFile(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "resolve_file", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !cur.Deleted {
		writeError(w, http.StatusConflict, "not_deleted", "file is not deleted")
		return
	}
	f := cur.Clone()
	f.Deleted = false
	f.Revision++
	f.Dates.Modified = time.Now().UTC()
	if _, err := s.appendEvent(r.Context(), &journal.Event{
		TenantID: s.cfg.Tenant,
		Type:     journal.FileRestored,
		Actor:    s.cfg.Actor,
		FileID:   f.FileID,
		State:    f,
	}); err != nil {
		s.fail(w, "append_event", err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(f))
}

type versionRequest struct {
	Upload           model.UploadItem `json:"upload"`
	ExpectedRevision int64            `json:"expected_revision"`
}

// newVersion replaces the bytes of a file with a new upload. The earlier
// object stays in the bucket and is listed under versions; this is the only
// edit path WORM mode allows. See requirement:local-drive-worm-audit-mode.
func (s *Server) newVersion(w http.ResponseWriter, r *http.Request) {
	var req versionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cur, ok, err := s.resolveFile(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "resolve_file", err)
		return
	}
	if !ok || cur.Deleted {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != cur.Revision {
		writeError(w, http.StatusConflict, "revision_conflict", fmt.Sprintf("file is at revision %d", cur.Revision))
		return
	}
	up := req.Upload
	if strings.TrimSpace(up.ObjectKey) == "" {
		writeError(w, http.StatusBadRequest, "missing_object_key", "upload.object_key is required")
		return
	}
	if up.Status != "" && up.Status != model.UploadUploaded {
		writeError(w, http.StatusConflict, "upload_not_complete", fmt.Sprintf("upload status is %q", up.Status))
		return
	}
	if up.ObjectKey == cur.ObjectKey {
		writeError(w, http.StatusConflict, "same_object", "the upload is already the current version")
		return
	}
	for _, v := range cur.Versions {
		if v.ObjectKey == up.ObjectKey {
			writeError(w, http.StatusConflict, "same_object", "the upload is already an earlier version of this file")
			return
		}
	}
	head, err := s.deps.Store.HeadObject(r.Context(), storage.HeadInput{Bucket: s.cfg.Bucket, Key: up.ObjectKey})
	if err != nil {
		if objerr.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "object_not_found", "the uploaded object does not exist")
			return
		}
		s.fail(w, "head_object", err)
		return
	}
	now := time.Now().UTC()
	eventID := journal.NewULID(now)
	f := cur.Clone()
	f.Versions = append(f.Versions, meta.Version{
		ObjectKey:      cur.ObjectKey,
		EventID:        eventID,
		At:             now,
		SizeBytes:      cur.SizeBytes,
		ChecksumSHA256: cur.ChecksumSHA256,
		ContentType:    cur.ContentType,
		Name:           cur.Name,
	})
	f.ObjectKey = up.ObjectKey
	f.DisplayKey = up.DisplayKey
	f.UploadKey = up.UploadKey
	f.SizeBytes = head.ContentLength
	f.ChecksumSHA256 = up.ChecksumSHA256
	if up.ContentType != "" {
		f.ContentType = up.ContentType
	} else if head.ContentType != "" {
		f.ContentType = head.ContentType
	}
	f.Protected = up.Protected
	f.Derived = meta.Derived{}
	if up.Thumbnail != nil {
		f.Derived.Thumbnail = meta.Asset{ObjectKey: up.Thumbnail.ObjectKey, ContentType: up.Thumbnail.ContentType, Status: up.Thumbnail.Status}
	}
	if up.ExtractedContent != nil {
		f.Derived.Text = meta.TextAsset{ObjectKey: up.ExtractedContent.ObjectKey, Status: up.ExtractedContent.Status}
	}
	if up.Preview != nil {
		f.Derived.Preview = meta.Asset{ObjectKey: up.Preview.ObjectKey, ContentType: up.Preview.ContentType, Status: up.Preview.Status}
	}
	// Extracted values belong to the old bytes; the indexer re-reads them.
	f.Author.Extracted = ""
	f.Dates.Created = nil
	f.Dates.Shot = nil
	if up.UploadedAt != nil && !up.UploadedAt.IsZero() {
		f.Dates.Uploaded = up.UploadedAt.UTC()
	} else {
		f.Dates.Uploaded = now
	}
	f.Dates.Modified = now
	f.Revision++
	facts, _ := json.Marshal(up)
	if _, err := s.appendEvent(r.Context(), &journal.Event{
		EventID:          eventID,
		At:               now,
		TenantID:         s.cfg.Tenant,
		Type:             journal.FileVersioned,
		Actor:            s.cfg.Actor,
		FileID:           f.FileID,
		State:            f,
		ExpectedRevision: req.ExpectedRevision,
		UploadFacts:      facts,
	}); err != nil {
		s.fail(w, "append_event", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.view(f))
}

type versionView struct {
	meta.Version
	N    int `json:"n"`
	URLs struct {
		Content  string `json:"content"`
		Download string `json:"download"`
	} `json:"urls"`
}

// listVersions returns the earlier objects of a file, oldest first.
func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	f, ok, err := s.resolveFile(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "resolve_file", err)
		return
	}
	if !ok || f.Deleted {
		writeError(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	out := make([]versionView, 0, len(f.Versions))
	for i, v := range f.Versions {
		vv := versionView{Version: v, N: i + 1}
		base := fmt.Sprintf("/api/drive/files/%s/versions/%d", f.FileID, i+1)
		vv.URLs.Content = base + "/content"
		vv.URLs.Download = base + "/download"
		out = append(out, vv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"file_id": f.FileID, "current_object_key": f.ObjectKey, "revision": f.Revision, "versions": out})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"tenant":           s.cfg.Tenant,
		"files_cached":     s.deps.Metas.Len(),
		"overlay_entries":  s.overlayEntryCount(),
		"last_journal_key": "",
	}
	if s.deps.Indexer != nil {
		out["last_journal_key"] = s.deps.Indexer.LastJournalKey()
	}
	if s.deps.Search != nil {
		if st, err := s.deps.Search.Stats(r.Context()); err == nil {
			out["index"] = st
		} else {
			out["index_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) reindex(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Indexer == nil {
		writeError(w, http.StatusServiceUnavailable, "no_indexer", "this server has no indexer")
		return
	}
	s.deps.Indexer.RebuildAsync()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "rebuild_scheduled"})
}

func (s *Server) fail(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.logger.Error("drive_"+op+"_failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", op+" failed")
}
