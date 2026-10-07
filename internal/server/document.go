package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"streamuploader/internal/docpreview"
	"streamuploader/internal/extraction"
	"streamuploader/internal/model"
	"streamuploader/internal/storage"
	"streamuploader/internal/thumbnail"
)

// documentPasswordHeader carries the password of a protected PDF/Office
// upload. It is only held in memory for the request and the conversion job;
// it is never stored or logged.
const documentPasswordHeader = "X-Document-Password"

type documentJob struct {
	password string
	// format is the bdf format Verify confirmed.
	format string
	// done is set when the conversion already ran before the upload was
	// accepted (fail_upload_on_error).
	done *docpreview.Result
}

func (s *Server) documentUpload(contentType, fileName string) bool {
	return docpreview.SupportedFor(contentType, fileName)
}

func (s *Server) readStoredObject(ctx context.Context, key string, limit int64) ([]byte, error) {
	out, err := s.store.GetObject(ctx, storage.GetInput{Bucket: s.cfg.Bucket, Key: key})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("document exceeds %d bytes", limit)
	}
	return body, nil
}

// prepareDocument runs before the staged upload is committed. It rejects a
// protected document without (or with a wrong) password, and converts the
// document up front when conversion failures must cancel the upload.
func (s *Server) prepareDocument(ctx context.Context, stagedKey, contentType, fileName, format, password string) (job *documentJob, protected, fallback bool, err error) {
	policy := s.cfg.DocumentProcessing
	input, err := s.readStoredObject(ctx, stagedKey, policy.MaxInputBytes)
	if err != nil {
		return nil, false, false, s.documentFailure(err)
	}
	protected, err = docpreview.CheckPassword(input, password)
	if err != nil && (protected || policy.FailUploadOnError) {
		return nil, protected, false, s.documentFailure(err)
	}
	if err != nil {
		// Not a password problem: the document is broken. Without
		// fail_upload_on_error the upload stays accepted and the derived
		// assets are marked failed by the conversion job.
		protected = false
	}
	// bdf's own detection must confirm the content (not just the name or
	// the declared type) before the upload is treated as this format.
	confirmed, err := docpreview.Verify(input, format, protected)
	switch {
	case errors.Is(err, docpreview.ErrNotConfirmed):
		return nil, false, true, nil
	case err != nil:
		return nil, protected, false, s.documentFailure(err)
	}
	job = &documentJob{password: password, format: confirmed}
	if policy.FailUploadOnError {
		result, err := docpreview.Convert(input, contentType, docpreview.Options{FileName: fileName, Format: confirmed, Password: password, ThumbnailSize: max(s.cfg.Thumbnails.Width, s.cfg.Thumbnails.Height)})
		if err != nil {
			return nil, protected, false, s.documentFailure(err)
		}
		job.done = result
	}
	return job, protected, false, nil
}

func (s *Server) documentFailure(err error) error {
	switch {
	case errors.Is(err, docpreview.ErrPasswordRequired):
		return securityUploadError{status: http.StatusUnprocessableEntity, code: "document_password_required", message: "document is password protected; resend it with the " + documentPasswordHeader + " header"}
	case errors.Is(err, docpreview.ErrWrongPassword):
		return securityUploadError{status: http.StatusUnprocessableEntity, code: "document_password_invalid", message: "document password is wrong"}
	}
	if errors.Is(err, docpreview.ErrFormatMismatch) {
		return securityUploadError{status: http.StatusUnsupportedMediaType, code: "document_format_mismatch", message: err.Error()}
	}
	return securityUploadError{status: http.StatusUnprocessableEntity, code: "document_processing_failed", message: "document could not be converted: " + err.Error()}
}

func (s *Server) documentAssets(item *model.UploadItem, protected bool) {
	item.Protected = protected
	item.Thumbnail = &model.DerivedAsset{
		Kind:      "image_thumbnail",
		ObjectKey: item.ObjectKey + s.cfg.Thumbnails.ObjectKeySuffix,
		URL:       s.thumbnailURL(item.ObjectKey),
		Status:    "pending",
	}
	item.ExtractedContent = &model.DerivedAsset{
		Kind:        "extracted_content",
		ObjectKey:   extraction.ArtifactObjectKey(item.ObjectKey, s.cfg.TextExtraction),
		ContentType: "application/json; charset=utf-8",
		Status:      "pending",
	}
	item.Preview = &model.DerivedAsset{
		Kind:        "document_preview",
		ObjectKey:   docpreview.ObjectKey(item.ObjectKey),
		URL:         s.previewURL(item.ObjectKey),
		ContentType: docpreview.ContentType,
		Status:      "pending",
	}
	if protected {
		item.Thumbnail.Status = "locked"
		item.ExtractedContent.Status = "locked"
	}
}

var documentTaskKinds = []string{"image_thumbnail", "text_extraction", "document_preview"}

func (s *Server) scheduleDocument(uploadKey, objectKey, contentType string, job *documentJob, uploadCtx context.Context) *model.UploadItem {
	if s.cfg.DocumentProcessing.ExecutionMode == "sequential" {
		return s.runDocument(uploadKey, uploadCtx, contentType, job)
	}
	for _, kind := range documentTaskKinds {
		if err := s.putAsyncTaskMarker(context.Background(), asyncTaskMarker{ObjectKey: objectKey, Kind: kind, Status: "running"}); err != nil {
			slog.Warn("async_task_marker_create_failed", "upload_key", uploadKey, "object_key", objectKey, "kind", kind, "error", err)
		}
	}
	go s.runDocument(uploadKey, context.Background(), contentType, job)
	item, _ := s.upload(uploadKey)
	return item
}

func (s *Server) runDocument(uploadKey string, ctx context.Context, contentType string, job *documentJob) *model.UploadItem {
	item, ok := s.upload(uploadKey)
	if !ok || item == nil {
		return item
	}
	if s.cfg.DocumentProcessing.ExecutionMode == "async" {
		defer func() {
			for _, kind := range documentTaskKinds {
				s.deleteAsyncTaskMarker(context.Background(), item.ObjectKey, kind)
			}
		}()
	}
	opts := docpreview.Options{FileName: item.OriginalName, Format: job.format, Password: job.password, ThumbnailSize: max(s.cfg.Thumbnails.Width, s.cfg.Thumbnails.Height)}
	var thumb image.Image
	result := job.done
	if result != nil {
		thumb = result.Thumbnail
	} else {
		input, err := s.readStoredObject(ctx, item.ObjectKey, s.cfg.DocumentProcessing.MaxInputBytes)
		if err != nil {
			return s.failDocument(uploadKey, err)
		}
		session, err := docpreview.Start(input, contentType, opts)
		if err != nil {
			return s.failDocument(uploadKey, err)
		}
		// The thumbnail is stored as soon as the first page is ready; the
		// remaining pages are converted afterwards.
		if thumb, err = session.Thumbnail(); err != nil {
			s.updateThumbnailForUpload(uploadKey, thumbnail.Result{}, err)
		} else if thumb != nil {
			s.storeDocumentThumbnail(ctx, uploadKey, item.ObjectKey, thumb)
		}
		if result, err = session.Finish(); err != nil {
			return s.failDocument(uploadKey, err)
		}
		result.Thumbnail = thumb
	}
	var updated *model.UploadItem
	if err := s.storeDocumentPreview(ctx, item.ObjectKey, result.BDF); err != nil {
		updated = s.updateDocumentPreview(uploadKey, int64(len(result.BDF)), err)
	} else {
		updated = s.updateDocumentPreview(uploadKey, int64(len(result.BDF)), nil)
	}
	if result.Protected {
		return updated
	}
	if job.done != nil {
		updated = s.storeDocumentThumbnail(ctx, uploadKey, item.ObjectKey, thumb)
	}
	return s.storeDocumentText(ctx, uploadKey, item.ObjectKey, contentType, job.format, result, updated)
}

func (s *Server) failDocument(uploadKey string, err error) *model.UploadItem {
	slog.Warn("document_processing_failed", "upload_key", uploadKey, "error", err)
	var updated *model.UploadItem
	s.updateUpload(uploadKey, func(current *model.UploadItem) {
		current.UpdatedAt = time.Now().UTC()
		for _, asset := range []*model.DerivedAsset{current.Thumbnail, current.ExtractedContent, current.Preview} {
			if asset != nil && asset.Status == "pending" {
				asset.Status = "failed"
				asset.Error = err.Error()
			}
		}
		updated = cloneUpload(current)
	})
	if updated != nil {
		s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: updated.Status, Item: updated})
	}
	return updated
}

func (s *Server) storeDocumentPreview(ctx context.Context, objectKey string, body []byte) error {
	_, err := s.store.PutObject(ctx, storage.PutInput{
		Bucket:      s.cfg.Bucket,
		Key:         docpreview.ObjectKey(objectKey),
		Body:        bytes.NewReader(body),
		ContentType: docpreview.ContentType,
		Metadata:    map[string]string{"source-object-key": objectKey, "kind": "document_preview"},
	})
	return err
}

func (s *Server) updateDocumentPreview(uploadKey string, size int64, err error) *model.UploadItem {
	var updated *model.UploadItem
	s.updateUpload(uploadKey, func(current *model.UploadItem) {
		if current.Preview == nil {
			return
		}
		current.UpdatedAt = time.Now().UTC()
		current.Preview.SizeBytes = size
		if err != nil {
			current.Preview.Status = "failed"
			current.Preview.Error = err.Error()
		} else {
			current.Preview.Status = "generated"
		}
		updated = cloneUpload(current)
	})
	if err != nil {
		slog.Warn("document_preview_failed", "upload_key", uploadKey, "error", err)
	}
	if updated != nil {
		s.broadcast(model.WatchServerMessage{Type: "state", UploadKey: uploadKey, Status: updated.Status, Item: updated})
	}
	return updated
}

func (s *Server) storeDocumentThumbnail(ctx context.Context, uploadKey, objectKey string, img image.Image) *model.UploadItem {
	conversion, err := thumbnail.EncodeImage(img, s.cfg.Thumbnails)
	if err == nil {
		conversion.ObjectKey = objectKey + s.cfg.Thumbnails.ObjectKeySuffix
		conversion.SourceObjectKey = objectKey
		err = thumbnail.StoreConversion(ctx, s.store, s.cfg.Bucket, conversion)
	}
	return s.updateThumbnailForUpload(uploadKey, conversion.Result, err)
}

func (s *Server) storeDocumentText(ctx context.Context, uploadKey, objectKey, contentType, format string, result *docpreview.Result, previous *model.UploadItem) *model.UploadItem {
	// "extracted" is the key the previous PDF/OOXML extractors used.
	content := extraction.Content{
		Texts:    map[string]string{},
		Sources:  map[string]extraction.Source{"extracted": {Backend: "bdf", ContentType: contentType}},
		Metadata: map[string]interface{}{},
	}
	// The joined text and the per-page text share the output budget.
	budget := int(s.cfg.TextExtraction.MaxOutputBytes / 4)
	text := truncateUTF8(docpreview.PlainText(result.Text), budget)
	if len(text) < len(docpreview.PlainText(result.Text)) {
		source := content.Sources["extracted"]
		source.Warnings = append(source.Warnings, "text_truncated")
		content.Sources["extracted"] = source
	}
	if result.Text != nil {
		remaining := budget
		for _, view := range result.Text.Views {
			for _, page := range view.Pages {
				if remaining <= 0 {
					break
				}
				pageText := truncateUTF8(page.Text, remaining)
				remaining -= len(pageText)
				content.Pages = append(content.Pages, extraction.PageText{View: view.ID, Page: page.Page, Text: pageText})
			}
		}
	}
	if strings.TrimSpace(text) != "" {
		content.Texts["extracted"] = text
	}
	if result.Text != nil {
		dc := result.Text.Meta.DC
		if len(dc.Title) > 0 && strings.TrimSpace(dc.Title[0]) != "" {
			content.Texts["title"] = dc.Title[0]
			content.Sources["title"] = extraction.Source{Backend: "bdf", ContentType: contentType}
		}
		if len(dc.Description) > 0 && strings.TrimSpace(dc.Description[0]) != "" {
			content.Texts["description"] = dc.Description[0]
			content.Sources["description"] = extraction.Source{Backend: "bdf", ContentType: contentType}
		}
		if raw, err := json.Marshal(result.Text.Meta); err == nil {
			var meta map[string]interface{}
			if json.Unmarshal(raw, &meta) == nil {
				content.Metadata["document"] = meta
			}
		}
	}
	if docpreview.TextFormat(format) {
		// CSV, Markdown, HTML and draw.io keep the text extraction of the
		// text path ("text", external commands, ...); bdf adds the pages.
		delete(content.Texts, "extracted")
		delete(content.Sources, "extracted")
		s.mergeTextExtraction(ctx, objectKey, &content)
	}
	extracted := extraction.Result{Status: "generated", Content: content}
	body, err := extraction.Marshal(content, s.cfg.TextExtraction)
	if err == nil {
		artifactKey := extraction.ArtifactObjectKey(objectKey, s.cfg.TextExtraction)
		_, err = s.store.PutObject(ctx, storage.PutInput{
			Bucket:      s.cfg.Bucket,
			Key:         artifactKey,
			Body:        bytes.NewReader(body),
			ContentType: "application/json; charset=utf-8",
			Metadata:    map[string]string{"source-object-key": objectKey, "kind": "extracted_content"},
		})
		extracted.ObjectKey = artifactKey
		extracted.SizeBytes = int64(len(body))
	}
	return s.updateTextExtractionForUpload(uploadKey, extracted, err)
}

// truncateUTF8 cuts text to at most limit bytes on a rune boundary.
func truncateUTF8(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

// mergeTextExtraction adds what the text extractor finds in the original
// file to content. Failures leave content as it is: the bdf text stays.
func (s *Server) mergeTextExtraction(ctx context.Context, objectKey string, content *extraction.Content) {
	input, err := s.readStoredObject(ctx, objectKey, s.cfg.DocumentProcessing.MaxInputBytes)
	if err != nil {
		return
	}
	policy := s.cfg.TextExtraction
	policy.MaxOutputBytes = s.cfg.TextExtraction.MaxOutputBytes / 2 // the pages use a quarter
	legacy, err := extraction.Generate(ctx, objectKey, "text/plain", bytes.NewReader(input), policy)
	if err != nil {
		slog.Warn("text_extraction_merge_failed", "object_key", objectKey, "error", err)
		return
	}
	for key, value := range legacy.Content.Texts {
		if _, ok := content.Texts[key]; !ok {
			content.Texts[key] = value
		}
	}
	for key, value := range legacy.Content.Sources {
		if _, ok := content.Sources[key]; !ok {
			content.Sources[key] = value
		}
	}
	for key, value := range legacy.Content.Metadata {
		if _, ok := content.Metadata[key]; !ok {
			content.Metadata[key] = value
		}
	}
}
