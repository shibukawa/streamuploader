package server

// The wire types of the Drive API. The framework generator plans these from
// their declared field types and never reflects at request time, so they are
// flat: times travel as RFC 3339 strings, optional sub-objects are value
// fields with omitzero, and nothing is a pointer. meta.File keeps its own
// shape for the journal and the snapshots; fileView is what a client sees.

import (
	"time"

	"github.com/shibukawa/tinybind-go/jsonbind"

	"streamuploader/drive/meta"
	"streamuploader/drive/sidecar"
)

type healthView struct {
	Status string `json:"status"`
}

// --- inputs -----------------------------------------------------------------

// assetFacts is one derived asset as streamuploader reported it.
type assetFacts struct {
	Kind        string `json:"kind"`
	ObjectKey   string `json:"object_key"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Status      string `json:"status"`
}

// uploadFacts is the upload record a client hands back from streamuploader
// when it registers a file. Only what the Drive reads is declared; the rest
// of the record is skipped by the binder.
type uploadFacts struct {
	UploadKey        string     `json:"upload_key"`
	OriginalName     string     `json:"original_name"`
	ContentType      string     `json:"content_type"`
	SizeBytes        int64      `json:"size_bytes"`
	ChecksumSHA256   string     `json:"checksum_sha256"`
	ObjectKey        string     `json:"object_key"`
	DisplayKey       string     `json:"display_key"`
	Status           string     `json:"status"`
	UploadedAt       string     `json:"uploaded_at"`
	Thumbnail        assetFacts `json:"thumbnail"`
	ExtractedContent assetFacts `json:"extracted_content"`
	Preview          assetFacts `json:"preview"`
	Protected        bool       `json:"protected"`
}

// The facts are recorded on the file_created event, so they need an encoder
// as well as the decoder the binder uses.
var _ = jsonbind.GenerateEncoder[uploadFacts]()

type locationInput struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// registerInput is the body of POST /api/drive/files.
type registerInput struct {
	Upload   uploadFacts   `payload:"upload"`
	Name     string        `payload:"name"`
	Tags     []string      `payload:"tags"`
	Author   string        `payload:"author"`
	Location locationInput `payload:"location"`
}

// patchInput is the body of PATCH /api/drive/files/{id}. The editable
// members arrive in Fields, because a patch distinguishes an absent member
// from an empty one: a missing name leaves the name alone, an empty tags list
// clears the tags.
type patchInput struct {
	ExpectedRevision int64          `payload:"expected_revision"`
	ClearLocation    bool           `payload:"clear_location"`
	Fields           map[string]any `payload:"*"`
}

// searchInput is the query of GET /api/drive/search.
type searchInput struct {
	Q      string   `query:"q"`
	Tag    []string `query:"tag"`
	Facet  []string `query:"facet"`
	Exact  string   `query:"exact"`
	Limit  int      `query:"limit"`
	Offset int      `query:"offset"`
	Sort   string   `query:"sort"`
}

// facetsInput is the query of GET /api/drive/facets.
type facetsInput struct {
	Path  string   `query:"path"`
	Q     string   `query:"q"`
	Tag   []string `query:"tag"`
	Facet []string `query:"facet"`
	Exact string   `query:"exact"`
}

// --- views ------------------------------------------------------------------

// fileView is the API shape of a file: the state plus the URLs a client needs.
type fileView struct {
	TenantID       string        `json:"tenant_id"`
	FileID         string        `json:"file_id"`
	Revision       int64         `json:"revision"`
	Name           string        `json:"name"`
	ContentType    string        `json:"content_type,omitempty"`
	SizeBytes      int64         `json:"size_bytes,omitzero"`
	ChecksumSHA256 string        `json:"checksum_sha256,omitempty"`
	ObjectKey      string        `json:"object_key"`
	DisplayKey     string        `json:"display_key,omitempty"`
	UploadKey      string        `json:"upload_key,omitempty"`
	Derived        derivedView   `json:"derived"`
	Tags           []string      `json:"tags"`
	Author         authorView    `json:"author"`
	Location       locationView  `json:"location,omitzero"`
	Dates          datesView     `json:"dates"`
	Versions       []versionView `json:"versions,omitempty"`
	Deleted        bool          `json:"deleted,omitzero"`
	Protected      bool          `json:"protected,omitzero"`
	LastEvent      string        `json:"last_event,omitempty"`
	Facets         []string      `json:"facets"`
	URLs           fileURLs      `json:"urls"`

	// file is the state the view was built from, kept for sorting.
	file *meta.File
}

type derivedView struct {
	Thumbnail assetView     `json:"thumbnail"`
	Text      textAssetView `json:"text"`
	Preview   assetView     `json:"preview"`
}

type assetView struct {
	ObjectKey   string `json:"object_key,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Status      string `json:"status,omitempty"`
}

type textAssetView struct {
	ObjectKey   string `json:"object_key,omitempty"`
	Status      string `json:"status,omitempty"`
	IndexedETag string `json:"indexed_etag,omitempty"`
}

type authorView struct {
	Extracted string `json:"extracted,omitempty"`
	Override  string `json:"override,omitempty"`
}

type locationView struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Source  string  `json:"source,omitempty"`
	Geohash string  `json:"geohash,omitempty"`
}

type datesView struct {
	Created  string `json:"created,omitempty"`
	Shot     string `json:"shot,omitempty"`
	Uploaded string `json:"uploaded"`
	Modified string `json:"modified"`
}

type versionView struct {
	ObjectKey string `json:"object_key"`
	EventID   string `json:"event_id"`
	At        string `json:"at"`
	SizeBytes int64  `json:"size_bytes,omitzero"`
}

type fileURLs struct {
	Content   string `json:"content"`
	Download  string `json:"download"`
	Preview   string `json:"preview,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

type searchHit struct {
	File    fileView `json:"file"`
	Score   float64  `json:"score"`
	Page    int64    `json:"page,omitzero"`
	View    string   `json:"view,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
	// Fresh marks a result that came from the journal overlay rather than
	// the index.
	Fresh bool `json:"fresh,omitzero"`
}

type searchResponse struct {
	Total   int         `json:"total"`
	Hits    []searchHit `json:"hits"`
	Query   string      `json:"query"`
	Facets  []string    `json:"facets"`
	Exact   bool        `json:"exact"`
	Sort    string      `json:"sort"`
	Overlay int         `json:"overlay"`
}

type facetChild struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type facetsResponse struct {
	Path     string       `json:"path"`
	Children []facetChild `json:"children"`
}

type indexStatsView struct {
	NumDocs       int64  `json:"num_docs"`
	Segments      int    `json:"segments"`
	SchemaVersion string `json:"schema_version"`
}

type statsResponse struct {
	Tenant         string         `json:"tenant"`
	FilesCached    int            `json:"files_cached"`
	OverlayEntries int            `json:"overlay_entries"`
	LastJournalKey string         `json:"last_journal_key"`
	Index          indexStatsView `json:"index,omitzero"`
	IndexError     string         `json:"index_error,omitempty"`
}

type reindexResponse struct {
	Status string `json:"status"`
}

// --- conversions ------------------------------------------------------------

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}

func (s *Server) view(f *meta.File) fileView {
	base := "/api/drive/files/" + f.FileID
	v := fileView{
		TenantID:       f.TenantID,
		FileID:         f.FileID,
		Revision:       f.Revision,
		Name:           f.Name,
		ContentType:    f.ContentType,
		SizeBytes:      f.SizeBytes,
		ChecksumSHA256: f.ChecksumSHA256,
		ObjectKey:      f.ObjectKey,
		DisplayKey:     f.DisplayKey,
		UploadKey:      f.UploadKey,
		Derived: derivedView{
			Thumbnail: assetView{ObjectKey: f.Derived.Thumbnail.ObjectKey, ContentType: f.Derived.Thumbnail.ContentType, Status: f.Derived.Thumbnail.Status},
			Text:      textAssetView{ObjectKey: f.Derived.Text.ObjectKey, Status: f.Derived.Text.Status, IndexedETag: f.Derived.Text.IndexedETag},
			Preview:   assetView{ObjectKey: f.Derived.Preview.ObjectKey, ContentType: f.Derived.Preview.ContentType, Status: f.Derived.Preview.Status},
		},
		Tags:   nonNil(f.Tags),
		Author: authorView{Extracted: f.Author.Extracted, Override: f.Author.Override},
		Dates: datesView{
			Created:  formatTimePtr(f.Dates.Created),
			Shot:     formatTimePtr(f.Dates.Shot),
			Uploaded: f.Dates.Uploaded.Format(time.RFC3339Nano),
			Modified: f.Dates.Modified.Format(time.RFC3339Nano),
		},
		Deleted:   f.Deleted,
		Protected: f.Protected,
		LastEvent: f.LastEvent,
		Facets:    sidecar.FacetPaths(f),
		URLs:      fileURLs{Content: base + "/content", Download: base + "/download"},
		file:      f,
	}
	if f.Location != nil {
		v.Location = locationView{Lat: f.Location.Lat, Lon: f.Location.Lon, Source: f.Location.Source, Geohash: f.Location.Geohash}
	}
	for _, version := range f.Versions {
		v.Versions = append(v.Versions, versionView{ObjectKey: version.ObjectKey, EventID: version.EventID, At: version.At.Format(time.RFC3339Nano), SizeBytes: version.SizeBytes})
	}
	if f.Derived.Preview.ObjectKey != "" {
		v.URLs.Preview = base + "/preview"
	}
	if f.Derived.Thumbnail.ObjectKey != "" {
		v.URLs.Thumbnail = base + "/thumbnail"
	}
	return v
}
