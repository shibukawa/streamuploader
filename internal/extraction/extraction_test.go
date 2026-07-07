package extraction

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"streamuploader/internal/config"
)

func resetPlan(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		Configure(config.DefaultSecurityPolicy().TextExtraction)
	})
}

func TestGeneratePlainTextUsesTextKey(t *testing.T) {
	resetPlan(t)
	Configure(config.TextExtractionPolicy{Enabled: true})
	result, err := Generate(context.Background(), "uploads/a.txt", "text/plain", bytes.NewBufferString("hello\n"), config.TextExtractionPolicy{
		MaxInputBytes:  1024,
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "generated" || result.Content.Texts["text"] != "hello" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGeneratePDFUsesPDFToTextWhenAvailable(t *testing.T) {
	resetPlan(t)
	if runtime.GOOS == "windows" {
		t.Skip("test creates a POSIX shell script")
	}
	dir := t.TempDir()
	pdftotext := filepath.Join(dir, "pdftotext")
	if err := os.WriteFile(pdftotext, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'from pdftotext\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	policy := config.TextExtractionPolicy{
		Enabled:          true,
		MaxInputBytes:    1024,
		MaxOutputBytes:   1024,
		ExternalTimeout:  config.DefaultSecurityPolicy().TextExtraction.ExternalTimeout,
		ExtractMetadata:  false,
		IncludePlainText: true,
	}
	Configure(policy)
	t.Setenv("PATH", t.TempDir())

	result, err := Generate(context.Background(), "uploads/a.pdf", "application/pdf", bytes.NewBufferString("%PDF-1.4\n(ignored literal)\n"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content.Texts["extracted"] != "from pdftotext" || result.Content.Sources["extracted"].Backend != "pdftotext" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGeneratePDFFallsBackWhenPDFToTextMissing(t *testing.T) {
	resetPlan(t)
	t.Setenv("PATH", t.TempDir())
	policy := config.TextExtractionPolicy{
		Enabled:         true,
		MaxInputBytes:   1024,
		MaxOutputBytes:  1024,
		ExternalTimeout: config.DefaultSecurityPolicy().TextExtraction.ExternalTimeout,
	}
	Configure(policy)
	result, err := Generate(context.Background(), "uploads/a.pdf", "application/pdf", bytes.NewBufferString("%PDF-1.4\n(fallback literal)\n"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content.Texts["extracted"] != "fallback literal" || result.Content.Sources["extracted"].Backend != "pdf_literal_parser" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGenerateOOXMLUsesExtractedAndMetadataKeys(t *testing.T) {
	resetPlan(t)
	Configure(config.TextExtractionPolicy{Enabled: true})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeZipFile(t, zw, "word/document.xml", `<w:document><w:body><w:p><w:r><w:t>document body</w:t></w:r></w:p></w:body></w:document>`)
	writeZipFile(t, zw, "docProps/core.xml", `<cp:coreProperties xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Doc Title</dc:title><dc:description>Doc Description</dc:description></cp:coreProperties>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := Generate(context.Background(), "uploads/a.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", bytes.NewReader(buf.Bytes()), config.TextExtractionPolicy{
		MaxInputBytes:   1024 * 1024,
		MaxOutputBytes:  1024 * 1024,
		ExtractMetadata: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content.Texts["extracted"] != "document body" || result.Content.Texts["title"] != "Doc Title" || result.Content.Texts["description"] != "Doc Description" {
		t.Fatalf("texts = %+v", result.Content.Texts)
	}
}

func writeZipFile(t *testing.T, zw *zip.Writer, name, body string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}
