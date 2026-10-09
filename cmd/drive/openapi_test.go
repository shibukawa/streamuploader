package main

import (
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pw"
)

// The Drive binary links the upload API and the Drive API, each with its own
// generated OpenAPI fragment; the served document is their merge, and a merge
// that fails is a 500 on /openapi.json rather than a build error.
func TestOpenAPIDocumentAssembles(t *testing.T) {
	if err := pw.SetOpenAPIInfo(pw.OpenAPIInfo{Title: "streamuploader Drive API", Version: "test"}); err != nil {
		t.Fatal(err)
	}
	document, err := pw.AssembleOpenAPI()
	if err != nil {
		t.Fatalf("assemble OpenAPI: %v", err)
	}
	// The upload API registers its routes under a configured base path, so
	// those patterns are not literals and stay out of the document; the Drive
	// routes are literal and must be there.
	for _, path := range []string{`"/api/drive/search"`, `"/api/drive/files"`} {
		if !strings.Contains(string(document), path) {
			t.Errorf("document lacks %s", path)
		}
	}
}
