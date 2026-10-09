package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/drive/journal"
	"streamuploader/drive/meta"
	"streamuploader/drive/objerr"
	"streamuploader/drive/sidecar"
	"streamuploader/internal/model"
	"streamuploader/internal/storage"
)

func (s *Server) registerFile(w http.ResponseWriter, r *http.Request) {
	req, err := pw.Parse[registerInput](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	up := req.Upload
	if strings.TrimSpace(up.ObjectKey) == "" {
		writeProblem(w, r, http.StatusBadRequest, "missing_object_key", "upload.object_key is required")
		return
	}
	if up.Status != "" && up.Status != string(model.UploadUploaded) {
		writeProblem(w, r, http.StatusConflict, "upload_not_complete", fmt.Sprintf("upload status is %q", up.Status))
		return
	}
	head, err := s.deps.Store.HeadObject(r.Context(), storage.HeadInput{Bucket: s.cfg.Bucket, Key: up.ObjectKey})
	if err != nil {
		if objerr.IsNotFound(err) {
			writeProblem(w, r, http.StatusNotFound, "object_not_found", "the uploaded object does not exist")
			return
		}
		s.fail(w, r, "head_object", err)
		return
	}
	tags, err := meta.NormalizeTags(req.Tags)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_tag", err.Error())
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
	if uploadedAt, err := time.Parse(time.RFC3339Nano, up.UploadedAt); err == nil && !uploadedAt.IsZero() {
		f.Dates.Uploaded = uploadedAt.UTC()
	}
	if up.Thumbnail.ObjectKey != "" || up.Thumbnail.Status != "" {
		f.Derived.Thumbnail = meta.Asset{ObjectKey: up.Thumbnail.ObjectKey, ContentType: up.Thumbnail.ContentType, Status: up.Thumbnail.Status}
	}
	if up.ExtractedContent.ObjectKey != "" || up.ExtractedContent.Status != "" {
		f.Derived.Text = meta.TextAsset{ObjectKey: up.ExtractedContent.ObjectKey, Status: up.ExtractedContent.Status}
	}
	if up.Preview.ObjectKey != "" || up.Preview.Status != "" {
		f.Derived.Preview = meta.Asset{ObjectKey: up.Preview.ObjectKey, ContentType: up.Preview.ContentType, Status: up.Preview.Status}
	}
	if req.Location != (locationInput{}) {
		f.Location = &meta.Location{Lat: req.Location.Lat, Lon: req.Location.Lon, Source: "user", Geohash: sidecar.Geohash(req.Location.Lat, req.Location.Lon, 8)}
	}
	if _, err := s.appendEvent(r.Context(), &journal.Event{
		TenantID:    s.cfg.Tenant,
		Type:        journal.FileCreated,
		Actor:       s.cfg.Actor,
		FileID:      f.FileID,
		State:       f,
		UploadFacts: up.AppendJSONTo(nil),
	}); err != nil {
		s.fail(w, r, "append_event", err)
		return
	}
	pw.WriteStatus(w, r, http.StatusCreated, s.view(f))
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	f, ok, err := s.resolveFile(r.Context(), pw.PathValue(r, "id"))
	if err != nil {
		s.fail(w, r, "resolve_file", err)
		return
	}
	if !ok || f.Deleted {
		writeProblem(w, r, http.StatusNotFound, "not_found", "file not found")
		return
	}
	pw.WriteAPI(w, r, s.view(f))
}

func (s *Server) patchFile(w http.ResponseWriter, r *http.Request) {
	req, err := pw.Parse[patchInput](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	cur, ok, err := s.resolveFile(r.Context(), pw.PathValue(r, "id"))
	if err != nil {
		s.fail(w, r, "resolve_file", err)
		return
	}
	if !ok || cur.Deleted {
		writeProblem(w, r, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != cur.Revision {
		writeProblem(w, r, http.StatusConflict, "revision_conflict", fmt.Sprintf("file is at revision %d", cur.Revision))
		return
	}
	f := cur.Clone()
	if raw, ok := req.Fields["name"]; ok {
		name, ok := raw.(string)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid_json", "name must be a string")
			return
		}
		name = strings.TrimSpace(name)
		if name == "" {
			writeProblem(w, r, http.StatusBadRequest, "invalid_name", "name must not be empty")
			return
		}
		f.Name = name
	}
	if raw, ok := req.Fields["tags"]; ok {
		values, ok := stringList(raw)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid_json", "tags must be a list of strings")
			return
		}
		tags, err := meta.NormalizeTags(values)
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "invalid_tag", err.Error())
			return
		}
		f.Tags = tags
	}
	if raw, ok := req.Fields["author"]; ok {
		author, ok := raw.(string)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid_json", "author must be a string")
			return
		}
		f.Author.Override = strings.TrimSpace(author)
	}
	if req.ClearLocation {
		f.Location = nil
	} else if raw, ok := req.Fields["location"]; ok && raw != nil {
		lat, lon, ok := locationFields(raw)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid_json", "location must carry numeric lat and lon")
			return
		}
		f.Location = &meta.Location{Lat: lat, Lon: lon, Source: "user", Geohash: sidecar.Geohash(lat, lon, 8)}
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
		s.fail(w, r, "append_event", err)
		return
	}
	pw.WriteAPI(w, r, s.view(f))
}

// stringList reads a decoded JSON array of strings.
func stringList(raw any) ([]string, bool) {
	items, ok := raw.([]any)
	if !ok {
		return nil, raw == nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

// locationFields reads lat and lon out of a decoded JSON object.
func locationFields(raw any) (float64, float64, bool) {
	object, ok := raw.(map[string]any)
	if !ok {
		return 0, 0, false
	}
	lat, ok := object["lat"].(float64)
	if !ok {
		return 0, 0, false
	}
	lon, ok := object["lon"].(float64)
	if !ok {
		return 0, 0, false
	}
	return lat, lon, true
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	cur, ok, err := s.resolveFile(r.Context(), pw.PathValue(r, "id"))
	if err != nil {
		s.fail(w, r, "resolve_file", err)
		return
	}
	if !ok || cur.Deleted {
		writeProblem(w, r, http.StatusNotFound, "not_found", "file not found")
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
		s.fail(w, r, "append_event", err)
		return
	}
	pw.SetRoute(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	out := statsResponse{
		Tenant:         s.cfg.Tenant,
		FilesCached:    s.deps.Metas.Len(),
		OverlayEntries: s.overlayEntryCount(),
	}
	if s.deps.Indexer != nil {
		out.LastJournalKey = s.deps.Indexer.LastJournalKey()
	}
	if s.deps.Search != nil {
		if st, err := s.deps.Search.Stats(r.Context()); err == nil {
			out.Index = indexStatsView{NumDocs: st.NumDocs, Segments: st.Segments, SchemaVersion: st.SchemaVersion}
		} else {
			out.IndexError = err.Error()
		}
	}
	pw.WriteAPI(w, r, out)
}

func (s *Server) reindex(w http.ResponseWriter, r *http.Request) {
	if s.deps.Indexer == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "no_indexer", "this server has no indexer")
		return
	}
	s.deps.Indexer.RebuildAsync()
	pw.WriteStatus(w, r, http.StatusAccepted, reindexResponse{Status: "rebuild_scheduled"})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, io.EOF) {
		writeProblem(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	pw.Logger(r).Error("drive_"+op+"_failed", pw.Err(err))
	writeProblem(w, r, http.StatusInternalServerError, "internal_error", op+" failed")
}
