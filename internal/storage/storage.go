package storage

import (
	"context"
	"io"
	"time"
)

type PutInput struct {
	Bucket      string
	Key         string
	Body        io.Reader
	ContentType string
	Metadata    map[string]string
	// Retention, when set, asks the store to lock the object (Object Lock).
	Retention *Retention
}

type PutResult struct {
	ETag string
}

type CopyInput struct {
	Bucket      string
	SourceKey   string
	Key         string
	ContentType string
	Metadata    map[string]string
	// Retention, when set, locks the copy (Object Lock).
	Retention *Retention
}

type CopyResult struct {
	ETag string
}

type GetInput struct {
	Bucket string
	Key    string
	Range  string
}

type GetResult struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ContentRange  string
	ETag          string
	LastModified  time.Time
	Metadata      map[string]string
}

type HeadInput struct {
	Bucket string
	Key    string
}

type HeadResult struct {
	ContentType   string
	ContentLength int64
	ETag          string
	LastModified  time.Time
	Metadata      map[string]string
}

type ListInput struct {
	Bucket string
	Prefix string
	// StartAfter returns only keys that sort after this key, as S3
	// ListObjectsV2 StartAfter does. Empty lists from the beginning.
	StartAfter string
	// MaxKeys bounds the number of keys returned; zero returns all keys.
	MaxKeys int
}

type ListResult struct {
	Keys []string
}

type PresignGetInput struct {
	Bucket                     string
	Key                        string
	Expires                    time.Duration
	ResponseContentDisposition string
}

type PresignGetResult struct {
	URL       string
	ExpiresAt time.Time
}

type DeleteInput struct {
	Bucket string
	Key    string
}

type Store interface {
	PutObject(ctx context.Context, input PutInput) (PutResult, error)
	CopyObject(ctx context.Context, input CopyInput) (CopyResult, error)
	GetObject(ctx context.Context, input GetInput) (GetResult, error)
	HeadObject(ctx context.Context, input HeadInput) (HeadResult, error)
	ListObjects(ctx context.Context, input ListInput) (ListResult, error)
	PresignGetObject(ctx context.Context, input PresignGetInput) (PresignGetResult, error)
	DeleteObject(ctx context.Context, input DeleteInput) error
}
