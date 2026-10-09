package server

// The wire types of the Drive API. The framework generator plans these from
// their declared field types and never reflects at request time, so they are
// flat: times travel as RFC 3339 strings, optional sub-objects are value
// fields with omitzero, and nothing is a pointer. meta.File keeps its own
// shape for the journal and the snapshots; fileView is what a client sees.

import (
	"bytes"
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
// when it registers a file or a new version. Only what the Drive reads is
// declared; the rest of the record is skipped by the binder.
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

// The facts are recorded on the file_created and file_versioned events, so
// they need an encoder as well as the decoder the binder uses.
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

// versionInput is the body of POST /api/drive/files/{id}/versions.
type versionInput struct {
	Upload           uploadFacts `payload:"upload"`
	ExpectedRevision int64       `payload:"expected_revision"`
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

// versionView is an earlier object of a file, as it appears inside a file.
type versionView struct {
	ObjectKey      string `json:"object_key"`
	EventID        string `json:"event_id"`
	At             string `json:"at"`
	SizeBytes      int64  `json:"size_bytes,omitzero"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	Name           string `json:"name,omitempty"`
}

// versionEntryView is one row of GET /api/drive/files/{id}/versions: the
// version plus its 1-based number and the URLs that serve it.
type versionEntryView struct {
	ObjectKey      string      `json:"object_key"`
	EventID        string      `json:"event_id"`
	At             string      `json:"at"`
	SizeBytes      int64       `json:"size_bytes,omitzero"`
	ChecksumSHA256 string      `json:"checksum_sha256,omitempty"`
	ContentType    string      `json:"content_type,omitempty"`
	Name           string      `json:"name,omitempty"`
	N              int         `json:"n"`
	URLs           versionURLs `json:"urls"`
}

type versionURLs struct {
	Content  string `json:"content"`
	Download string `json:"download"`
}

type versionsResponse struct {
	FileID           string             `json:"file_id"`
	CurrentObjectKey string             `json:"current_object_key"`
	Revision         int64              `json:"revision"`
	Versions         []versionEntryView `json:"versions"`
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

// indexerStatusView is the state of whoever keeps the index: the in-process
// indexer (mode indexer) or the snapshot follower (mode server). Members
// that only one of them reports are omitted for the other.
type indexerStatusView struct {
	Mode             string `json:"mode"`
	Snapshot         bool   `json:"snapshot,omitzero"`
	Generation       int64  `json:"generation"`
	LastJournalKey   string `json:"last_journal_key,omitempty"`
	PendingText      int    `json:"pending_text,omitzero"`
	Unpublished      int    `json:"unpublished,omitzero"`
	LastPublishError string `json:"last_publish_error,omitempty"`
	Refreshes        int    `json:"refreshes,omitzero"`
	PublishedAt      string `json:"published_at,omitempty"`
	RefreshedAt      string `json:"refreshed_at,omitempty"`
	NumDocs          int64  `json:"num_docs,omitzero"`
	Files            int    `json:"files,omitzero"`
	LastError        string `json:"last_error,omitempty"`
}

type statsResponse struct {
	Tenant         string            `json:"tenant"`
	FilesCached    int               `json:"files_cached"`
	OverlayEntries int               `json:"overlay_entries"`
	LastJournalKey string            `json:"last_journal_key"`
	Indexer        indexerStatusView `json:"indexer,omitzero"`
	Index          indexStatsView    `json:"index,omitzero"`
	IndexError     string            `json:"index_error,omitempty"`
}

type reindexResponse struct {
	Status string `json:"status"`
}

// infoView is GET /api/drive/info.
type infoView struct {
	Tenant   string   `json:"tenant"`
	Delivery string   `json:"delivery"`
	Actor    string   `json:"actor"`
	WORM     wormView `json:"worm"`
}

type wormView struct {
	Mode         string         `json:"mode"`
	Enabled      bool           `json:"enabled"`
	Enforcement  string         `json:"enforcement"`
	AccessWindow string         `json:"access_window"`
	ReadLogging  string         `json:"read_logging"`
	PresignTTL   string         `json:"presign_ttl"`
	ObjectLock   objectLockView `json:"object_lock"`
}

type objectLockView struct {
	Mode        string `json:"mode"`
	Description string `json:"description"`
	Retention   string `json:"retention,omitempty"`
}

// checkpointSummary is one row of GET /api/drive/checkpoints.
type checkpointSummary struct {
	N            int64     `json:"n"`
	Key          string    `json:"key"`
	At           string    `json:"at"`
	EventCount   int       `json:"event_count"`
	Originals    int       `json:"originals"`
	JournalRange rangeView `json:"journal_range"`
	MerkleRoot   string    `json:"journal_merkle_root"`
	Prev         string    `json:"prev_checkpoint_sha256"`
}

type rangeView struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

type checkpointsResponse struct {
	Tenant      string              `json:"tenant"`
	Total       int                 `json:"total"`
	Genesis     string              `json:"genesis"`
	Checkpoints []checkpointSummary `json:"checkpoints"`
}

// storedDocument is a JSON document the Drive stored earlier and answers
// with byte for byte (an audit checkpoint), so a client verifies the same
// bytes the chain hashed. It carries its own codec: the bytes are the
// document.
type storedDocument []byte

// AppendJSONTo implements jsonbind.Appender.
func (d storedDocument) AppendJSONTo(dst []byte) []byte {
	trimmed := bytes.TrimSpace(d)
	if len(trimmed) == 0 {
		return append(dst, "null"...)
	}
	return append(dst, trimmed...)
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
		v.Versions = append(v.Versions, versionView{
			ObjectKey:      version.ObjectKey,
			EventID:        version.EventID,
			At:             version.At.Format(time.RFC3339Nano),
			SizeBytes:      version.SizeBytes,
			ChecksumSHA256: version.ChecksumSHA256,
			ContentType:    version.ContentType,
			Name:           version.Name,
		})
	}
	if f.Derived.Preview.ObjectKey != "" {
		v.URLs.Preview = base + "/preview"
	}
	if f.Derived.Thumbnail.ObjectKey != "" {
		v.URLs.Thumbnail = base + "/thumbnail"
	}
	return v
}

// viewOfIndexStatus reads the index keeper's status map into the typed view.
// The map is what the indexer and the follower publish; the keys are theirs.
func viewOfIndexStatus(status map[string]any) indexerStatusView {
	v := indexerStatusView{}
	v.Mode, _ = status["mode"].(string)
	v.Snapshot, _ = status["snapshot"].(bool)
	v.Generation = asInt64(status["generation"])
	v.LastJournalKey, _ = status["last_journal_key"].(string)
	v.PendingText = int(asInt64(status["pending_text"]))
	v.Unpublished = int(asInt64(status["unpublished"]))
	v.LastPublishError, _ = status["last_publish_error"].(string)
	v.Refreshes = int(asInt64(status["refreshes"]))
	v.PublishedAt = asTimeString(status["published_at"])
	v.RefreshedAt = asTimeString(status["refreshed_at"])
	v.NumDocs = asInt64(status["num_docs"])
	v.Files = int(asInt64(status["files"]))
	v.LastError, _ = status["last_error"].(string)
	return v
}

func asInt64(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case uint:
		return int64(n)
	case uint64:
		return int64(n)
	case float64:
		return int64(n)
	case bool:
		if n {
			return 1
		}
	}
	return 0
}

func asTimeString(value any) string {
	switch t := value.(type) {
	case time.Time:
		return formatTime(t)
	case *time.Time:
		return formatTimePtr(t)
	case string:
		return t
	}
	return ""
}
