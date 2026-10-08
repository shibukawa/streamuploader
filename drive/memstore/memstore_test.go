package memstore

import (
	"context"
	"io"
	"strings"
	"testing"

	"streamuploader/internal/storage"
)

func TestListStartAfterAndRange(t *testing.T) {
	ctx := context.Background()
	s := New()
	for _, k := range []string{"j/t/2026/10/A", "j/t/2026/10/B", "j/t/2026/11/C", "other"} {
		if _, err := s.PutObject(ctx, storage.PutInput{Bucket: "b", Key: k, Body: strings.NewReader("0123456789")}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.ListObjects(ctx, storage.ListInput{Bucket: "b", Prefix: "j/t/", StartAfter: "j/t/2026/10/A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Keys) != 2 || res.Keys[0] != "j/t/2026/10/B" {
		t.Fatalf("keys %v", res.Keys)
	}
	out, err := s.GetObject(ctx, storage.GetInput{Bucket: "b", Key: "other", Range: "bytes=2-4"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(out.Body)
	if string(body) != "234" || out.ContentRange != "bytes 2-4/10" {
		t.Fatalf("range body %q content-range %q", body, out.ContentRange)
	}
	if _, err := s.GetObject(ctx, storage.GetInput{Bucket: "b", Key: "missing"}); err == nil {
		t.Fatal("missing key returned no error")
	}
}
