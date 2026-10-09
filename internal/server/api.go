package server

// The wire types of the streamuploader HTTP API.
//
// Handlers read requests through pw.Parse and answer through pw.WriteAPI,
// pw.WriteStatus and pw.WriteProblem, all of which run on codecs that
// `go tool pw generate` writes for the types declared here. The generator
// plans a struct from its declared field types and never reflects at request
// time, so the types it is handed are flat: times travel as RFC 3339 strings,
// optional sub-objects are value fields with omitzero, and nothing is a
// pointer. The in-memory state (model.UploadItem and friends) keeps its own
// shape; these are the views of it a client sees and the inputs it sends.

import (
	"net/http"
	"time"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/internal/extraction"
	"streamuploader/internal/jsonany"
	"streamuploader/internal/model"
)

// writeProblem answers with an RFC 9457 problem document carrying
// streamuploader's stable error code. A 5xx keeps its code and message for the
// log only: the framework reports a generic "internal" problem to the client.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	pw.WriteProblem(w, r, pw.Problem{Status: status, Title: http.StatusText(status), Code: code, Message: message})
}

// --- inputs -----------------------------------------------------------------

// createUploadKeyInput is the body of POST {upload_base}/keys.
type createUploadKeyInput struct {
	FileName     string `payload:"file_name"`
	ContentType  string `payload:"content_type"`
	SizeBytes    int64  `payload:"size_bytes"`
	Role         string `payload:"role"`
	Prefix       string `payload:"prefix"`
	KeyNamespace string `payload:"key_namespace"`
}

// waitUploadsInput is the body of POST {upload_base}/wait.
type waitUploadsInput struct {
	UploadKeys     []string `payload:"upload_keys"`
	TimeoutSeconds int      `payload:"timeout_seconds"`
}

// waitAsyncTasksInput is the query of GET {backend_base}/tasks/wait. Both the
// singular and the plural spellings are accepted, each possibly comma
// separated.
type waitAsyncTasksInput struct {
	ObjectKey      []string `query:"object_key"`
	ObjectKeys     []string `query:"object_keys"`
	Kind           []string `query:"kind"`
	Kinds          []string `query:"kinds"`
	TimeoutSeconds int      `query:"timeout_seconds"`
	PollMillis     int      `query:"poll_millis"`
}

// extractedContentInput is the query of GET {backend_base}/objects/{objectKey}/extracted-content.
type extractedContentInput struct {
	Wait           bool     `query:"wait"`
	TimeoutSeconds int      `query:"timeout_seconds"`
	PollMillis     int      `query:"poll_millis"`
	StatusOnly     bool     `query:"status_only"`
	Include        []string `query:"include"`
}

// presignInput is the body of POST {backend_base}/file/presigned-url.
type presignInput struct {
	ObjectKey  string `payload:"object_key"`
	FileName   string `payload:"file_name"`
	TTLSeconds int    `payload:"ttl_seconds"`
}

// sharedKeyInput is the body of POST {backend_base}/file/shared-keys.
type sharedKeyInput struct {
	ObjectKey   string `payload:"object_key"`
	FileName    string `payload:"file_name"`
	ContentType string `payload:"content_type"`
	CreatedBy   string `payload:"created_by"`
	ExpiresAt   string `payload:"expires_at"`
	TTLSeconds  int    `payload:"ttl_seconds"`
}

// extractedContentPresignInput is the body of POST {backend_base}/objects/{objectKey}/extracted-content/presigned-url.
type extractedContentPresignInput struct {
	TTLSeconds           int  `payload:"ttl_seconds"`
	Wait                 bool `payload:"wait"`
	IncludePendingStatus bool `payload:"include_pending_status"`
}

// --- views ------------------------------------------------------------------

type healthView struct {
	Status string `json:"status"`
}

// uploadView is one upload as the API reports it.
type uploadView struct {
	UploadKey        string           `json:"upload_key"`
	Role             string           `json:"role,omitempty"`
	OriginalName     string           `json:"original_name"`
	ContentType      string           `json:"content_type,omitempty"`
	SizeBytes        int64            `json:"size_bytes,omitzero"`
	UploadedBytes    int64            `json:"uploaded_bytes"`
	ChecksumSHA256   string           `json:"checksum_sha256,omitempty"`
	StoragePrefix    string           `json:"storage_prefix"`
	ObjectKey        string           `json:"object_key"`
	DisplayKey       string           `json:"display_key"`
	Status           string           `json:"status"`
	Error            string           `json:"error,omitempty"`
	CreatedAt        string           `json:"created_at"`
	UpdatedAt        string           `json:"updated_at"`
	ExpiresAt        string           `json:"expires_at"`
	UploadedAt       string           `json:"uploaded_at,omitempty"`
	Thumbnail        derivedAssetView `json:"thumbnail,omitzero"`
	ExtractedContent derivedAssetView `json:"extracted_content,omitzero"`
	Preview          derivedAssetView `json:"preview,omitzero"`
	Protected        bool             `json:"protected,omitzero"`
}

// derivedAssetView describes one asset generated from an upload.
type derivedAssetView struct {
	Kind        string `json:"kind"`
	ObjectKey   string `json:"object_key"`
	URL         string `json:"url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Width       int    `json:"width,omitzero"`
	Height      int    `json:"height,omitzero"`
	SizeBytes   int64  `json:"size_bytes,omitzero"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type createUploadKeyView struct {
	UploadKey      string `json:"upload_key"`
	ExpiresAt      string `json:"expires_at"`
	UploadURL      string `json:"upload_url"`
	StoragePrefix  string `json:"storage_prefix"`
	ObjectKey      string `json:"object_key"`
	DisplayKey     string `json:"display_key"`
	MaxUploadBytes int64  `json:"max_upload_bytes"`
}

type waitUploadsView struct {
	Ready   bool         `json:"ready"`
	Timeout bool         `json:"timeout"`
	Items   []uploadView `json:"items"`
}

type asyncTaskView struct {
	ObjectKey string `json:"object_key"`
	Kind      string `json:"kind"`
	Pending   bool   `json:"pending"`
}

type waitAsyncTasksView struct {
	Ready   bool            `json:"ready"`
	Timeout bool            `json:"timeout"`
	Tasks   []asyncTaskView `json:"tasks"`
}

// extractedContentStatus describes an extraction artifact without carrying
// it: the pending, skipped and status-only answers.
type extractedContentStatus struct {
	ObjectKey         string          `json:"object_key"`
	ArtifactObjectKey string          `json:"artifact_object_key"`
	Status            string          `json:"status"`
	Tasks             []asyncTaskView `json:"tasks,omitempty"`
	ErrorCode         string          `json:"error_code,omitempty"`
}

// extractedContentResponse carries the extracted content itself.
type extractedContentResponse struct {
	ObjectKey         string               `json:"object_key"`
	ArtifactObjectKey string               `json:"artifact_object_key"`
	Status            string               `json:"status"`
	Content           extractedContentView `json:"content"`
	Tasks             []asyncTaskView      `json:"tasks,omitempty"`
}

// extractedContentView is extraction.Content as the API reports it. Metadata
// is free-form, so it travels through jsonany.Map, which carries its own
// codec; it is always present, as an empty object when nothing was extracted.
type extractedContentView struct {
	Texts    map[string]string     `json:"texts,omitempty"`
	Sources  map[string]sourceView `json:"sources,omitempty"`
	Metadata jsonany.Map           `json:"metadata"`
	Pages    []pageTextView        `json:"pages,omitempty"`
}

type sourceView struct {
	Backend     string   `json:"backend,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type pageTextView struct {
	View string `json:"view,omitempty"`
	Page int    `json:"page,omitzero"`
	Text string `json:"text"`
}

type presignView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

type extractedPresignView struct {
	ArtifactObjectKey string `json:"artifact_object_key"`
	URL               string `json:"url"`
	ExpiresAt         string `json:"expires_at"`
	Status            string `json:"status"`
}

type sharedKeyView struct {
	SharedKey   string `json:"shared_key"`
	DownloadURL string `json:"download_url"`
	ExpiresAt   string `json:"expires_at"`
}

// watchClientMessage is what a watcher sends over the WebSocket.
type watchClientMessage struct {
	Type       string   `json:"type"`
	UploadKeys []string `json:"upload_keys"`
	ClientID   string   `json:"client_id"`
}

// watchServerMessage is what the server pushes to a watcher.
type watchServerMessage struct {
	Type          string     `json:"type"`
	UploadKey     string     `json:"upload_key,omitempty"`
	Item          uploadView `json:"item,omitzero"`
	UploadedBytes int64      `json:"uploaded_bytes,omitzero"`
	SizeBytes     int64      `json:"size_bytes,omitzero"`
	Status        string     `json:"status,omitempty"`
	Code          string     `json:"code,omitempty"`
	Message       string     `json:"message,omitempty"`
}

// --- conversions ------------------------------------------------------------

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func viewOfUpload(item *model.UploadItem) uploadView {
	if item == nil {
		return uploadView{}
	}
	v := uploadView{
		UploadKey:      item.UploadKey,
		Role:           item.Role,
		OriginalName:   item.OriginalName,
		ContentType:    item.ContentType,
		SizeBytes:      item.SizeBytes,
		UploadedBytes:  item.UploadedBytes,
		ChecksumSHA256: item.ChecksumSHA256,
		StoragePrefix:  item.StoragePrefix,
		ObjectKey:      item.ObjectKey,
		DisplayKey:     item.DisplayKey,
		Status:         string(item.Status),
		Error:          item.Error,
		CreatedAt:      item.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      item.UpdatedAt.Format(time.RFC3339Nano),
		ExpiresAt:      item.ExpiresAt.Format(time.RFC3339Nano),
		Protected:      item.Protected,
	}
	if item.UploadedAt != nil {
		v.UploadedAt = formatTime(*item.UploadedAt)
	}
	v.Thumbnail = viewOfDerivedAsset(item.Thumbnail)
	v.ExtractedContent = viewOfDerivedAsset(item.ExtractedContent)
	v.Preview = viewOfDerivedAsset(item.Preview)
	return v
}

func viewOfDerivedAsset(asset *model.DerivedAsset) derivedAssetView {
	if asset == nil {
		return derivedAssetView{}
	}
	return derivedAssetView{
		Kind:        asset.Kind,
		ObjectKey:   asset.ObjectKey,
		URL:         asset.URL,
		ContentType: asset.ContentType,
		Width:       asset.Width,
		Height:      asset.Height,
		SizeBytes:   asset.SizeBytes,
		Status:      asset.Status,
		Error:       asset.Error,
	}
}

func viewsOfUploads(items []*model.UploadItem) []uploadView {
	out := make([]uploadView, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, viewOfUpload(item))
	}
	return out
}

func viewsOfAsyncTasks(tasks []model.WaitAsyncTaskStatus) []asyncTaskView {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]asyncTaskView, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, asyncTaskView{ObjectKey: task.ObjectKey, Kind: task.Kind, Pending: task.Pending})
	}
	return out
}

func viewOfExtractedContent(content extraction.Content) extractedContentView {
	v := extractedContentView{Texts: content.Texts, Metadata: jsonany.Map(content.Metadata)}
	if len(content.Sources) > 0 {
		v.Sources = make(map[string]sourceView, len(content.Sources))
		for name, source := range content.Sources {
			v.Sources[name] = sourceView{Backend: source.Backend, ContentType: source.ContentType, Warnings: source.Warnings}
		}
	}
	if len(content.Pages) > 0 {
		v.Pages = make([]pageTextView, 0, len(content.Pages))
		for _, page := range content.Pages {
			v.Pages = append(v.Pages, pageTextView{View: page.View, Page: page.Page, Text: page.Text})
		}
	}
	return v
}

func viewOfWatchMessage(msg model.WatchServerMessage) watchServerMessage {
	return watchServerMessage{
		Type:          msg.Type,
		UploadKey:     msg.UploadKey,
		Item:          viewOfUpload(msg.Item),
		UploadedBytes: msg.UploadedBytes,
		SizeBytes:     msg.SizeBytes,
		Status:        string(msg.Status),
		Code:          msg.Code,
		Message:       msg.Message,
	}
}

// writeInvalidRequest answers a request the binder could not read.
func writeInvalidRequest(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, http.StatusBadRequest, "invalid_request", err.Error())
}
