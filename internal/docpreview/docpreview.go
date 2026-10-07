// Package docpreview converts PDF, OOXML Office, Photoshop, Illustrator, EPUB, Parquet and Visio (.vsdx) files in-process with
// github.com/shibukawa/bdf. One conversion yields the full-document preview
// (.bdf), a thumbnail image and the search text.
package docpreview

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"mime"
	"path/filepath"
	"strings"

	"github.com/shibukawa/bdf"
	"github.com/shibukawa/bdf/converter"
	_ "github.com/shibukawa/bdf/converter/ai"
	_ "github.com/shibukawa/bdf/converter/docx"
	_ "github.com/shibukawa/bdf/converter/epub"
	_ "github.com/shibukawa/bdf/converter/parquet"
	_ "github.com/shibukawa/bdf/converter/pdf"
	_ "github.com/shibukawa/bdf/converter/pptx"
	_ "github.com/shibukawa/bdf/converter/psd"
	_ "github.com/shibukawa/bdf/converter/visio"
	_ "github.com/shibukawa/bdf/converter/xlsx"
	"github.com/shibukawa/bdf/thumbnail"
)

const (
	// ContentType is the media type of a stored single-file BDF preview.
	ContentType = "application/x-bdf"
	// Extension is the object key suffix of a stored preview.
	Extension = ".bdf"

	pdfType  = "application/pdf"
	docxType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	xlsxType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	pptxType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

var (
	// ErrPasswordRequired means the document is protected and no password was given.
	ErrPasswordRequired = errors.New("document password required")
	// ErrWrongPassword means the given password does not open the document.
	ErrWrongPassword = errors.New("document password is wrong")
)

// Result is the output of one conversion.
type Result struct {
	// Protected is true when the input needed a password. Thumbnail and Text
	// are empty then: nothing readable is kept outside the sealed BDF.
	Protected bool
	// BDF is the single-file BDF of every page; sealed with the password when Protected.
	BDF []byte
	// Thumbnail is the first page; nil when Protected.
	Thumbnail image.Image
	// Text is the search text; nil when Protected.
	Text *bdf.SearchText
}

// Supported reports whether contentType is a PDF, OOXML Office or Photoshop type.
// Illustrator files need the file name too: see SupportedFor.
func Supported(contentType string) bool {
	return formatName(contentType) != ""
}

// SupportedFor is Supported with the original file name, which identifies
// Illustrator (.ai) files: they are PDF-compatible and are usually declared
// or detected as application/pdf, application/postscript or octet-stream.
func SupportedFor(contentType, fileName string) bool {
	return FormatFor(contentType, fileName) != ""
}

// FormatFor returns the bdf format name for an upload, or "".
func FormatFor(contentType, fileName string) string {
	if strings.EqualFold(filepath.Ext(fileName), ".ai") {
		switch baseType(contentType) {
		case "", pdfType, "application/postscript", "application/illustrator", "application/vnd.adobe.illustrator", "application/octet-stream":
			return "ai"
		}
	}
	if name := formatName(contentType); name != "" {
		return name
	}
	// Browsers often send no type, or octet-stream, for these.
	switch baseType(contentType) {
	case "", "application/octet-stream", "application/zip":
		switch strings.ToLower(filepath.Ext(fileName)) {
		case ".epub":
			return "epub"
		case ".parquet":
			return "parquet"
		case ".vsdx":
			return "visio"
		}
	}
	return ""
}

func baseType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(contentType)))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	return mediaType
}

func formatName(contentType string) string {
	switch baseType(contentType) {
	case pdfType:
		return "pdf"
	case docxType:
		return "docx"
	case xlsxType:
		return "xlsx"
	case pptxType:
		return "pptx"
	case "image/vnd.adobe.photoshop", "image/x-photoshop":
		return "psd"
	case "application/illustrator", "application/vnd.adobe.illustrator":
		return "ai"
	case "application/epub+zip":
		return "epub"
	case "application/vnd.apache.parquet", "application/x-parquet", "application/parquet":
		return "parquet"
	case "application/vnd.ms-visio.drawing.main+xml", "application/vnd.ms-visio.drawing":
		return "visio"
	}
	return ""
}

// CheckPassword tells whether the input is protected, without converting it.
// It returns ErrPasswordRequired or ErrWrongPassword for protected input.
func CheckPassword(input []byte, password string) (bool, error) {
	protected, err := converter.CheckPassword(bytes.NewReader(input), int64(len(input)), password)
	switch {
	case errors.Is(err, converter.ErrPasswordRequired):
		return true, ErrPasswordRequired
	case errors.Is(err, converter.ErrWrongPassword):
		return true, ErrWrongPassword
	}
	return protected, err
}

// Options tune a conversion.
type Options struct {
	// FileName is the original name; it identifies Illustrator files.
	FileName string
	// Format is the format Verify confirmed. When empty it is derived
	// from the content type and the file name.
	Format        string
	Password      string
	ThumbnailSize int
}

// Session converts a document in two steps: Thumbnail returns the first
// page as soon as it is ready (for a PDF without converting the others),
// then Finish converts the rest into the full document. Formats without
// page-wise conversion are converted whole by Start.
type Session struct {
	stream    converter.Stream
	protected bool
	opts      Options
}

// Start opens a document. It returns ErrPasswordRequired or
// ErrWrongPassword for protected input.
func Start(input []byte, contentType string, opts Options) (*Session, error) {
	name := opts.Format
	if name == "" {
		name = FormatFor(contentType, opts.FileName)
	}
	if name == "" {
		return nil, fmt.Errorf("docpreview: unsupported content type %q", contentType)
	}
	// Text formats cannot be recognized by bdf from their content alone.
	convertName := ""
	if TextFormat(name) {
		convertName = name
	}
	protected, err := CheckPassword(input, opts.Password)
	if err != nil {
		return nil, err
	}
	stream, err := converter.OpenStream(bytes.NewReader(input), int64(len(input)), convertName, &converter.Options{
		FileName: "document." + name,
		Password: opts.Password,
		// Never fetch images from the network (HTML, Markdown, EPUB): that
		// would let an upload make the server request arbitrary URLs.
		Params: map[string]string{"remote": "false"},
	})
	if err != nil {
		switch {
		case errors.Is(err, converter.ErrPasswordRequired):
			return nil, ErrPasswordRequired
		case errors.Is(err, converter.ErrWrongPassword):
			return nil, ErrWrongPassword
		}
		return nil, err
	}
	if protected && opts.Password == "" {
		return nil, ErrPasswordRequired
	}
	return &Session{stream: stream, protected: protected, opts: opts}, nil
}

// Protected reports whether the document needed a password.
func (s *Session) Protected() bool { return s.protected }

// Thumbnail draws the first page. It returns nil for a protected document:
// nothing readable is kept outside the sealed BDF.
func (s *Session) Thumbnail() (image.Image, error) {
	if s.protected {
		return nil, nil
	}
	doc := s.stream.Outline()
	if s.stream.Pages() > 0 {
		var err error
		if doc, err = s.stream.Page(0); err != nil {
			return nil, fmt.Errorf("docpreview: first page: %w", err)
		}
	}
	thumb, err := thumbnail.Make(doc, &thumbnail.Options{Size: s.opts.ThumbnailSize})
	if err != nil {
		return nil, fmt.Errorf("docpreview: thumbnail: %w", err)
	}
	return thumb.Image, nil
}

// Finish converts every page and returns the BDF and the search text. The
// session is done afterwards. Thumbnail in the result is nil; use the one
// from Thumbnail.
func (s *Session) Finish() (*Result, error) {
	res, err := s.stream.Finish()
	if err != nil {
		return nil, err
	}
	out := &Result{Protected: s.protected || res.Protected}
	if !out.Protected {
		text, err := res.Doc.SearchText()
		if err != nil {
			return nil, fmt.Errorf("docpreview: search text: %w", err)
		}
		out.Text = text
	} else {
		lock, err := bdf.NewPasswordLock(s.opts.Password, 0)
		if err != nil {
			return nil, err
		}
		res.Doc.Lock = lock
	}
	var buf bytes.Buffer
	if err := res.Doc.WriteSingle(&buf); err != nil {
		return nil, fmt.Errorf("docpreview: write bdf: %w", err)
	}
	out.BDF = buf.Bytes()
	return out, nil
}

// Convert runs both steps and returns everything at once.
func Convert(input []byte, contentType string, opts Options) (*Result, error) {
	session, err := Start(input, contentType, opts)
	if err != nil {
		return nil, err
	}
	thumb, err := session.Thumbnail()
	if err != nil {
		return nil, err
	}
	out, err := session.Finish()
	if err != nil {
		return nil, err
	}
	out.Thumbnail = thumb
	return out, nil
}

// PlainText joins the pages of a search text with blank lines.
func PlainText(st *bdf.SearchText) string {
	if st == nil {
		return ""
	}
	var b strings.Builder
	for _, view := range st.Views {
		for _, page := range view.Pages {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(page.Text)
		}
	}
	return b.String()
}

// ObjectKey returns the key of the preview stored next to a source object.
func ObjectKey(sourceKey string) string {
	return sourceKey + Extension
}
