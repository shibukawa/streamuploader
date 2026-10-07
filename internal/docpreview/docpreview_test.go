package docpreview

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shibukawa/bdf"
)

const protectedPassword = "パスワード🔑bdf"

func read(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestConvertSupportedTypes(t *testing.T) {
	cases := map[string]string{
		"sample.pdf":  pdfType,
		"sample.docx": docxType,
		"sample.xlsx": xlsxType,
		"sample.pptx": pptxType,
	}
	for name, contentType := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := Convert(read(t, name), contentType, Options{ThumbnailSize: 128})
			if err != nil {
				t.Fatal(err)
			}
			if res.Protected || res.Thumbnail == nil || res.Text == nil {
				t.Fatalf("unexpected result: protected=%v thumb=%v text=%v", res.Protected, res.Thumbnail != nil, res.Text != nil)
			}
			if b := res.Thumbnail.Bounds(); b.Dx() == 0 || b.Dy() == 0 || b.Dx() > 128 || b.Dy() > 128 {
				t.Fatalf("thumbnail bounds = %v", b)
			}
			doc, err := bdf.OpenSingle(bytes.NewReader(res.BDF), int64(len(res.BDF)))
			if err != nil {
				t.Fatalf("stored bdf does not open: %v", err)
			}
			if doc.Encrypted() {
				t.Fatal("unprotected document must not be encrypted")
			}
		})
	}
}

func TestConvertRejectsUnsupportedType(t *testing.T) {
	if Supported("application/msword") || Supported("application/vnd.oasis.opendocument.text") {
		t.Fatal("legacy Office and ODF must not be supported")
	}
	if _, err := Convert([]byte("x"), "application/msword", Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestProtectedDocument(t *testing.T) {
	input := read(t, "protected.pptx")
	if _, err := Convert(input, pptxType, Options{}); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := Convert(input, pptxType, Options{Password: "wrong"}); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	res, err := Convert(input, pptxType, Options{Password: protectedPassword})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Protected || res.Thumbnail != nil || res.Text != nil {
		t.Fatalf("protected document must not expose thumbnail or text: %+v", res)
	}
	reader, err := bdf.OpenSingle(bytes.NewReader(res.BDF), int64(len(res.BDF)))
	if err != nil {
		t.Fatal(err)
	}
	if !reader.Encrypted() || !reader.Locked() {
		t.Fatalf("bdf must be sealed: encrypted=%v locked=%v", reader.Encrypted(), reader.Locked())
	}
	if err := reader.Unlock("wrong"); err == nil {
		t.Fatal("wrong password unlocked the bdf")
	}
	if err := reader.Unlock(protectedPassword); err != nil {
		t.Fatalf("same password must unlock: %v", err)
	}
}

func TestPlainTextAndObjectKey(t *testing.T) {
	res, err := Convert(read(t, "sample.docx"), docxType, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(PlainText(res.Text)) == "" {
		t.Fatal("expected search text")
	}
	if ObjectKey("a/b.pdf") != "a/b.pdf.bdf" {
		t.Fatal("unexpected object key")
	}
}

func TestPhotoshopAndIllustrator(t *testing.T) {
	cases := []struct{ file, contentType string }{
		{"sample.psd", "image/vnd.adobe.photoshop"},
		{"sample.ai", "application/illustrator"},
		{"sample.ai", "application/pdf"},
		{"sample.ai", "application/postscript"},
		{"sample.ai", ""},
	}
	for _, tc := range cases {
		t.Run(tc.file+"/"+tc.contentType, func(t *testing.T) {
			if !SupportedFor(tc.contentType, tc.file) {
				t.Fatal("not supported")
			}
			res, err := Convert(read(t, tc.file), tc.contentType, Options{FileName: tc.file, ThumbnailSize: 64})
			if err != nil {
				t.Fatal(err)
			}
			if res.Thumbnail == nil || len(res.BDF) == 0 {
				t.Fatalf("thumbnail=%v bdf=%d", res.Thumbnail != nil, len(res.BDF))
			}
		})
	}
	if SupportedFor("application/pdf", "report.pdf") && FormatFor("application/pdf", "report.pdf") != "pdf" {
		t.Fatal("plain PDF must stay pdf")
	}
	if _, err := Convert(read(t, "legacy.ai"), "application/postscript", Options{FileName: "legacy.ai"}); err == nil {
		t.Fatal("PostScript-only .ai cannot be converted")
	}
}

func TestStagedSessionThumbnailBeforeFinish(t *testing.T) {
	session, err := Start(read(t, "sample.pdf"), pdfType, Options{ThumbnailSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	thumb, err := session.Thumbnail()
	if err != nil || thumb == nil {
		t.Fatalf("thumbnail = %v, %v", thumb, err)
	}
	res, err := session.Finish()
	if err != nil || len(res.BDF) == 0 || res.Text == nil {
		t.Fatalf("finish = %+v, %v", res, err)
	}
}

func TestEPUBParquetVisio(t *testing.T) {
	cases := []struct{ file, contentType string }{
		{"sample.epub", "application/epub+zip"},
		{"sample.epub", ""},
		{"sample.parquet", "application/vnd.apache.parquet"},
		{"sample.parquet", "application/octet-stream"},
		{"sample.vsdx", "application/vnd.ms-visio.drawing.main+xml"},
		{"sample.vsdx", "application/vnd.ms-visio.drawing"},
	}
	for _, tc := range cases {
		t.Run(tc.file+"/"+tc.contentType, func(t *testing.T) {
			if !SupportedFor(tc.contentType, tc.file) {
				t.Fatal("not supported")
			}
			res, err := Convert(read(t, tc.file), tc.contentType, Options{FileName: tc.file, ThumbnailSize: 64})
			if err != nil {
				t.Fatal(err)
			}
			if res.Thumbnail == nil || len(res.BDF) == 0 {
				t.Fatalf("thumbnail=%v bdf=%d", res.Thumbnail != nil, len(res.BDF))
			}
		})
	}
	if SupportedFor("", "notes.txt") || SupportedFor("text/csv", "a.csv") || SupportedFor("text/plain", "a.drawio") {
		t.Fatal("text-like and unlisted types stay on the text path")
	}
}

const sampleCSV = "name,qty,price\napple,3,1.5\nbanana,12,0.5\ncherry,7,4\n"

func TestCandidateAndVerifyTextFormats(t *testing.T) {
	drawio := read(t, "sample.drawio")
	cases := []struct {
		name        string
		contentType string
		fileName    string
		body        []byte
		candidate   string
		verified    bool
	}{
		{"csv by extension", "text/plain", "a.csv", []byte(sampleCSV), "csv", true},
		{"tsv", "text/tab-separated-values", "a.tsv", []byte("a\tb\n1\t2\n3\t4\n"), "csv", true},
		{"semicolon csv", "", "a.csv", []byte("a;b;c\n1;2;3\n4;5;6\n"), "csv", true},
		{"prose named csv", "text/csv", "notes.csv", []byte("Just one sentence, nothing tabular.\n"), "csv", false},
		{"markdown", "text/plain", "readme.md", []byte("# Title\n\nbody\n"), "markdown", true},
		{"html by type", "text/html", "page.html", []byte("<!doctype html><html><body>hi</body></html>"), "html", true},
		{"html inside text/plain", "text/plain", "page", []byte("<!DOCTYPE html><html><body>hi</body></html>"), "html", true},
		{"html extension but prose", "text/html", "x.html", []byte("not markup at all"), "html", false},
		{"drawio inside text/plain", "text/plain", "diagram", drawio, "drawio", true},
		{"drawio by extension", "application/octet-stream", "d.drawio", drawio, "drawio", true},
		{"drawio extension, other content", "text/plain", "d.drawio", []byte("plain words"), "drawio", false},
		{"plain text stays plain", "text/plain", "notes.txt", []byte("hello\n"), "", false},
		{"binary is no text format", "image/png", "a.csv", []byte("\x89PNG"), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix := tc.body[:min(len(tc.body), 3072)]
			got := Candidate(tc.contentType, tc.fileName, prefix)
			if got != tc.candidate {
				t.Fatalf("candidate = %q, want %q", got, tc.candidate)
			}
			if got == "" {
				return
			}
			format, err := Verify(tc.body, got, false)
			if tc.verified && (err != nil || format != got) {
				t.Fatalf("verify = %q, %v", format, err)
			}
			if !tc.verified && !errors.Is(err, ErrNotConfirmed) {
				t.Fatalf("verify err = %v, want not confirmed", err)
			}
		})
	}
}

func TestConvertTextFormats(t *testing.T) {
	cases := []struct {
		format, fileName string
		body             []byte
	}{
		{"csv", "a.csv", []byte(sampleCSV)},
		{"markdown", "a.md", []byte("# Heading\n\nSome *text* here.\n")},
		{"html", "a.html", []byte("<!doctype html><html><head><title>T</title></head><body><p>hello html</p></body></html>")},
		{"drawio", "a.drawio", read(t, "sample.drawio")},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			format, err := Verify(tc.body, tc.format, false)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Convert(tc.body, "text/plain", Options{FileName: tc.fileName, Format: format, ThumbnailSize: 64})
			if err != nil {
				t.Fatal(err)
			}
			if res.Thumbnail == nil || len(res.BDF) == 0 {
				t.Fatalf("thumbnail=%v bdf=%d", res.Thumbnail != nil, len(res.BDF))
			}
		})
	}
}

func TestBinaryFormatsAreVerifiedByContent(t *testing.T) {
	docx, parquet, epub := read(t, "sample.docx"), read(t, "sample.parquet"), read(t, "sample.epub")
	for _, tc := range []struct {
		name, format string
		body         []byte
		ok           bool
	}{
		{"epub", "epub", epub, true},
		{"parquet", "parquet", parquet, true},
		{"visio", "visio", read(t, "sample.vsdx"), true},
		{"docx", "docx", docx, true},
		{"docx declared as xlsx", "xlsx", docx, false},
		{"zip of docx declared as epub", "epub", docx, false},
		{"parquet declared as visio", "visio", parquet, false},
		{"garbage declared as parquet", "parquet", []byte("PAR1 not really"), false},
		{"epub declared as pdf", "pdf", epub, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Verify(tc.body, tc.format, false)
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && !errors.Is(err, ErrFormatMismatch) {
				t.Fatalf("err = %v, want format mismatch", err)
			}
		})
	}
	if err := CheckPrefix("epub", docx[:200]); !errors.Is(err, ErrFormatMismatch) {
		t.Fatalf("docx prefix as epub: %v", err)
	}
	if err := CheckPrefix("epub", epub[:min(len(epub), 3072)]); err != nil {
		t.Fatalf("epub prefix: %v", err)
	}
	if err := CheckPrefix("parquet", []byte("PK\x03\x04")); !errors.Is(err, ErrFormatMismatch) {
		t.Fatalf("zip prefix as parquet: %v", err)
	}
}

func TestConversionNeverFetchesRemoteImages(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	page := []byte(`<!doctype html><html><body><p>x</p><img src="` + srv.URL + `/a.png"></body></html>`)
	md := []byte("![x](" + srv.URL + "/b.png)\n")
	for name, tc := range map[string]struct {
		format string
		body   []byte
	}{"html": {"html", page}, "markdown": {"markdown", md}} {
		if _, err := Convert(tc.body, "text/plain", Options{FileName: "a." + name, Format: tc.format, ThumbnailSize: 64}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("conversion made %d network requests", n)
	}
}
