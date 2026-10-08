// Package meta holds the Drive's file metadata record: the state carried in
// every journal event and written by the indexer as meta/{tenant}/{id}.json.
// See .knowledge/concepts/data/drive-file-meta.yaml.
package meta

import (
	"mime"
	"path"
	"strings"
	"time"
)

// File is the latest folded state of one file.
type File struct {
	TenantID       string    `json:"tenant_id"`
	FileID         string    `json:"file_id"`
	Revision       int64     `json:"revision"`
	Name           string    `json:"name"`
	ContentType    string    `json:"content_type,omitempty"`
	SizeBytes      int64     `json:"size_bytes,omitempty"`
	ChecksumSHA256 string    `json:"checksum_sha256,omitempty"`
	ObjectKey      string    `json:"object_key"`
	DisplayKey     string    `json:"display_key,omitempty"`
	UploadKey      string    `json:"upload_key,omitempty"`
	Derived        Derived   `json:"derived"`
	Tags           []string  `json:"tags"`
	Author         Author    `json:"author"`
	Location       *Location `json:"location,omitempty"`
	Dates          Dates     `json:"dates"`
	Versions       []Version `json:"versions,omitempty"`
	Deleted        bool      `json:"deleted,omitempty"`
	Protected      bool      `json:"protected,omitempty"`
	LastEvent      string    `json:"last_event,omitempty"`
}

// Derived points at the assets streamuploader generated for the original.
type Derived struct {
	Thumbnail Asset     `json:"thumbnail"`
	Text      TextAsset `json:"text"`
	Preview   Asset     `json:"preview"`
}

type Asset struct {
	ObjectKey   string `json:"object_key,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Status      string `json:"status,omitempty"`
}

type TextAsset struct {
	ObjectKey string `json:"object_key,omitempty"`
	Status    string `json:"status,omitempty"`
	// IndexedETag is the ETag of the .text.json the indexer last folded.
	IndexedETag string `json:"indexed_etag,omitempty"`
}

// Author keeps the extracted value and the user's override apart.
type Author struct {
	Extracted string `json:"extracted,omitempty"`
	Override  string `json:"override,omitempty"`
}

// Effective is the author shown and indexed: the override wins.
func (a Author) Effective() string {
	if strings.TrimSpace(a.Override) != "" {
		return strings.TrimSpace(a.Override)
	}
	return strings.TrimSpace(a.Extracted)
}

type Location struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Source  string  `json:"source,omitempty"`
	Geohash string  `json:"geohash,omitempty"`
}

type Dates struct {
	Created  *time.Time `json:"created,omitempty"`
	Shot     *time.Time `json:"shot,omitempty"`
	Uploaded time.Time  `json:"uploaded"`
	Modified time.Time  `json:"modified"`
}

type Version struct {
	ObjectKey string    `json:"object_key"`
	EventID   string    `json:"event_id"`
	At        time.Time `json:"at"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
}

// Ext is the lower-case extension without the dot, from the name or else
// from the content type.
func (f *File) Ext() string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(f.Name), "."))
	if ext != "" {
		return ext
	}
	if f.ContentType != "" {
		base := strings.ToLower(strings.TrimSpace(strings.Split(f.ContentType, ";")[0]))
		if ext, ok := wellKnownExt[base]; ok {
			return ext
		}
		if exts, err := mime.ExtensionsByType(base); err == nil && len(exts) > 0 {
			return strings.TrimPrefix(exts[0], ".")
		}
	}
	return ""
}

// wellKnownExt pins the extension of common types, because
// mime.ExtensionsByType returns the platform's list in an arbitrary order.
var wellKnownExt = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/gif":       "gif",
	"image/webp":      "webp",
	"image/avif":      "avif",
	"image/svg+xml":   "svg",
	"application/pdf": "pdf",
	"text/plain":      "txt",
	"text/csv":        "csv",
	"text/markdown":   "md",
	"text/html":       "html",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
}

// PrimaryDate is the date the date view groups by: the shot time of a photo,
// then the document's creation time, then the upload time.
func (f *File) PrimaryDate() time.Time {
	if f.Dates.Shot != nil && !f.Dates.Shot.IsZero() {
		return *f.Dates.Shot
	}
	if f.Dates.Created != nil && !f.Dates.Created.IsZero() {
		return *f.Dates.Created
	}
	return f.Dates.Uploaded
}

// Clone returns a deep copy so overlay patches never alias cached state.
func (f *File) Clone() *File {
	if f == nil {
		return nil
	}
	c := *f
	c.Tags = append([]string(nil), f.Tags...)
	c.Versions = append([]Version(nil), f.Versions...)
	if f.Location != nil {
		loc := *f.Location
		c.Location = &loc
	}
	if f.Dates.Created != nil {
		t := *f.Dates.Created
		c.Dates.Created = &t
	}
	if f.Dates.Shot != nil {
		t := *f.Dates.Shot
		c.Dates.Shot = &t
	}
	return &c
}

// SnapshotKey is where the indexer writes the latest state of a file.
func SnapshotKey(prefix, tenant, fileID string) string {
	return prefix + "meta/" + tenant + "/" + fileID + ".json"
}

// SnapshotPrefix lists every snapshot of a tenant.
func SnapshotPrefix(prefix, tenant string) string {
	return prefix + "meta/" + tenant + "/"
}
