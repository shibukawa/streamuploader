package model

import "time"

type UploadStatus string

const (
	UploadKeyCreated UploadStatus = "key_created"
	UploadUploading  UploadStatus = "uploading"
	UploadUploaded   UploadStatus = "uploaded"
	UploadFailed     UploadStatus = "failed"
	UploadExpired    UploadStatus = "expired"
)

type UploadItem struct {
	UploadKey      string       `json:"upload_key"`
	Role           string       `json:"role,omitempty"`
	OriginalName   string       `json:"original_name"`
	ContentType    string       `json:"content_type,omitempty"`
	SizeBytes      int64        `json:"size_bytes,omitempty"`
	UploadedBytes  int64        `json:"uploaded_bytes"`
	ChecksumSHA256 string       `json:"checksum_sha256,omitempty"`
	StoragePrefix  string       `json:"storage_prefix"`
	ObjectKey      string       `json:"object_key"`
	DisplayKey     string       `json:"display_key"`
	Status         UploadStatus `json:"status"`
	Error          string       `json:"error,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	ExpiresAt      time.Time    `json:"expires_at"`
	UploadedAt     *time.Time   `json:"uploaded_at,omitempty"`
}

type CreateUploadKeyRequest struct {
	FileName     string `json:"file_name"`
	ContentType  string `json:"content_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Role         string `json:"role,omitempty"`
	Prefix       string `json:"prefix,omitempty"`
	KeyNamespace string `json:"key_namespace,omitempty"`
}

type CreateUploadKeyResponse struct {
	UploadKey      string    `json:"upload_key"`
	ExpiresAt      time.Time `json:"expires_at"`
	UploadURL      string    `json:"upload_url"`
	StoragePrefix  string    `json:"storage_prefix"`
	ObjectKey      string    `json:"object_key"`
	DisplayKey     string    `json:"display_key"`
	MaxUploadBytes int64     `json:"max_upload_bytes"`
}

type WaitUploadsRequest struct {
	UploadKeys     []string `json:"upload_keys"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

type WaitUploadsResponse struct {
	Ready   bool          `json:"ready"`
	Timeout bool          `json:"timeout"`
	Items   []*UploadItem `json:"items"`
}

type WatchClientMessage struct {
	Type       string   `json:"type"`
	UploadKeys []string `json:"upload_keys,omitempty"`
	ClientID   string   `json:"client_id,omitempty"`
}

type WatchServerMessage struct {
	Type          string       `json:"type"`
	UploadKey     string       `json:"upload_key,omitempty"`
	Item          *UploadItem  `json:"item,omitempty"`
	UploadedBytes int64        `json:"uploaded_bytes,omitempty"`
	SizeBytes     int64        `json:"size_bytes,omitempty"`
	Status        UploadStatus `json:"status,omitempty"`
	Code          string       `json:"code,omitempty"`
	Message       string       `json:"message,omitempty"`
}
