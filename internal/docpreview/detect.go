package docpreview

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/shibukawa/bdf/converter"
	_ "github.com/shibukawa/bdf/converter/csv"
	_ "github.com/shibukawa/bdf/converter/drawio"
	"github.com/shibukawa/bdf/converter/html"
	_ "github.com/shibukawa/bdf/converter/markdown"
)

var (
	// ErrFormatMismatch means the content is not the format the upload
	// claims, so the upload must be refused.
	ErrFormatMismatch = errors.New("document content does not match its declared format")
	// ErrNotConfirmed means a text upload does not look like the text
	// format its name or type suggests; it is handled as plain text.
	ErrNotConfirmed = errors.New("text content is not confirmed as the suggested format")
)

// TextFormat reports whether format is one bdf reads from plain text. Their
// uploads keep the text extraction of the text path next to the preview.
func TextFormat(format string) bool {
	switch format {
	case "csv", "markdown", "html", "drawio":
		return true
	}
	return false
}

// Candidate guesses the bdf format of an upload before its body has been
// read: from the declared type, the file name and the first bytes. It only
// nominates; Verify decides once the whole file is there.
func Candidate(contentType, fileName string, prefix []byte) string {
	if f := FormatFor(contentType, fileName); f != "" {
		return f
	}
	base := baseType(contentType)
	textual := base == "" || strings.HasPrefix(base, "text/") || base == "application/octet-stream" ||
		strings.HasSuffix(base, "+xml") || base == "application/xml" || base == "application/vnd.jgraph.mxfile" || base == "application/x-drawio"
	if !textual {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".csv", ".tsv", ".tab":
		return "csv"
	case ".md", ".markdown", ".mdown":
		return "markdown"
	case ".html", ".htm", ".xhtml":
		return "html"
	case ".drawio", ".dio":
		return "drawio"
	}
	switch base {
	case "text/csv", "text/tab-separated-values":
		return "csv"
	case "text/markdown":
		return "markdown"
	case "text/html", "application/xhtml+xml":
		return "html"
	case "application/vnd.jgraph.mxfile", "application/x-drawio":
		return "drawio"
	}
	// A plain text, XML or untyped upload without a telling name: look inside.
	if ext == "" || ext == ".txt" || ext == ".xml" {
		switch {
		case bytes.Contains(prefix, []byte("<mxfile")) || bytes.Contains(prefix, []byte("<mxGraphModel")):
			return "drawio"
		case html.IsHTML(prefix):
			return "html"
		}
	}
	return ""
}

// CheckPrefix refuses an upload whose first bytes cannot belong to its
// candidate format, before the rest of the file is read.
func CheckPrefix(format string, prefix []byte) error {
	switch format {
	case "parquet":
		if !bytes.HasPrefix(prefix, []byte("PAR1")) {
			return fmt.Errorf("%w: parquet files start with PAR1", ErrFormatMismatch)
		}
	case "epub":
		// the first ZIP entry is "mimetype", stored uncompressed
		if !bytes.HasPrefix(prefix, []byte("PK\x03\x04")) || !bytes.Contains(prefix[:min(len(prefix), 200)], []byte("mimetypeapplication/epub+zip")) {
			return fmt.Errorf("%w: an EPUB starts with a mimetype entry", ErrFormatMismatch)
		}
	case "visio":
		if !bytes.HasPrefix(prefix, []byte("PK\x03\x04")) {
			return fmt.Errorf("%w: a .vsdx file is a ZIP package", ErrFormatMismatch)
		}
	case "psd":
		if !bytes.HasPrefix(prefix, []byte("8BPS")) {
			return fmt.Errorf("%w: Photoshop files start with 8BPS", ErrFormatMismatch)
		}
	}
	return nil
}

// Verify examines the whole input with bdf's own format detection and
// returns the format to convert it as. A binary format must be confirmed
// (ErrFormatMismatch otherwise); a text format that is not confirmed returns
// ErrNotConfirmed. A protected document cannot be examined before it is
// decrypted, so its candidate is trusted (protected true).
func Verify(input []byte, format string, protected bool) (string, error) {
	if protected {
		return format, nil
	}
	if TextFormat(format) {
		if !looksLikeText(input) {
			return "", ErrNotConfirmed
		}
		switch format {
		case "csv":
			if !looksLikeTable(input) {
				return "", ErrNotConfirmed
			}
		case "html":
			if !html.IsHTML(input[:min(len(input), 4096)]) && detectedName(input) != "html" {
				return "", ErrNotConfirmed
			}
		case "drawio":
			if detectedName(input) != "drawio" {
				return "", ErrNotConfirmed
			}
		}
		return format, nil
	}
	detected := detectedName(input)
	if detected == format {
		// bdf recognizes some formats by a magic number alone; look further.
		if err := checkStructure(input, format); err != nil {
			return "", err
		}
	}
	switch {
	case detected == format:
		return format, nil
	case (format == "pdf" && detected == "ai") || (format == "ai" && detected == "pdf"):
		return detected, nil // Illustrator files are PDFs with extra data
	case format == "psd" && detected == "psd":
		return format, nil
	}
	if detected == "" {
		return "", fmt.Errorf("%w: not recognized as %s", ErrFormatMismatch, format)
	}
	return "", fmt.Errorf("%w: looks like %s, not %s", ErrFormatMismatch, detected, format)
}

func detectedName(input []byte) string {
	if f := converter.Detect(bytes.NewReader(input), int64(len(input))); f != nil {
		return f.Name
	}
	return ""
}

func looksLikeText(input []byte) bool {
	return !bytes.Contains(input[:min(len(input), 8192)], []byte{0})
}

// looksLikeTable parses the start of the input as delimited text: some
// delimiter must give at least two columns on most of the first records.
func looksLikeTable(input []byte) bool {
	head := input[:min(len(input), 64<<10)]
	for _, delimiter := range []rune{',', '\t', ';'} {
		reader := csv.NewReader(bytes.NewReader(head))
		reader.Comma = delimiter
		reader.FieldsPerRecord = -1
		reader.LazyQuotes = true
		var counts []int
		longField := false
		for len(counts) < 100 {
			record, err := reader.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				break
			}
			counts = append(counts, len(record))
			for _, field := range record {
				longField = longField || len(field) > 200
			}
		}
		if len(counts) == 0 || counts[0] < 2 || longField {
			continue
		}
		// A single line is a table only when it clearly is a header row,
		// not a sentence with a comma in it.
		if len(counts) == 1 && counts[0] < 3 {
			continue
		}
		same := 0
		for _, n := range counts {
			if n == counts[0] {
				same++
			}
		}
		if same*10 >= len(counts)*8 {
			return true
		}
	}
	return false
}

// checkStructure looks past the magic number of the formats that bdf
// recognizes from their first and last bytes.
func checkStructure(input []byte, format string) error {
	switch format {
	case "parquet":
		// ... <metadata> <4-byte little-endian metadata length> PAR1
		if len(input) < 12 || !bytes.HasPrefix(input, []byte("PAR1")) || !bytes.HasSuffix(input, []byte("PAR1")) {
			return fmt.Errorf("%w: parquet magic numbers are missing", ErrFormatMismatch)
		}
		footer := int64(binary.LittleEndian.Uint32(input[len(input)-8 : len(input)-4]))
		if footer <= 0 || footer > int64(len(input))-12 {
			return fmt.Errorf("%w: parquet footer length is impossible", ErrFormatMismatch)
		}
	case "epub":
		zr, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
		if err != nil {
			return fmt.Errorf("%w: not a ZIP package", ErrFormatMismatch)
		}
		if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
			return fmt.Errorf("%w: the first entry of an EPUB is mimetype", ErrFormatMismatch)
		}
		hasPackage := false
		for _, f := range zr.File {
			name := strings.ToLower(f.Name)
			hasPackage = hasPackage || name == "meta-inf/container.xml" || strings.HasSuffix(name, ".opf")
		}
		if !hasPackage {
			return fmt.Errorf("%w: EPUB has no package document", ErrFormatMismatch)
		}
	}
	return nil
}
