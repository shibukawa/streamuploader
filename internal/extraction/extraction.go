package extraction

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"streamuploader/internal/config"
)

type Content struct {
	Texts    map[string]string      `json:"texts,omitempty"`
	Sources  map[string]Source      `json:"sources,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type Source struct {
	Backend     string   `json:"backend,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type Result struct {
	Content     Content
	Status      string
	ErrorCode   string
	ObjectKey   string
	SizeBytes   int64
	HasText     bool
	HasOCR      bool
	HasMetadata bool
}

type Plan struct {
	Enabled           bool
	PDFToTextPath     string
	ExternalCommand   []string
	OCRCommand        []string
	ExternalAvailable bool
	OCRAvailable      bool
	Summary           string
	UnavailableNotes  []string
}

type ToolSummary struct {
	Kind      string
	Backend   string
	Available bool
	Path      string
}

var (
	activePlanMu sync.RWMutex
	activePlan   Plan
)

func Configure(policy config.TextExtractionPolicy) Plan {
	plan := ProbePlan(policy)
	activePlanMu.Lock()
	activePlan = plan
	activePlanMu.Unlock()
	return plan
}

func ProbePlan(policy config.TextExtractionPolicy) Plan {
	plan := Plan{Enabled: policy.Enabled}
	if !policy.Enabled {
		plan.Summary = "disabled"
		return plan
	}
	if path, err := exec.LookPath("pdftotext"); err == nil {
		plan.PDFToTextPath = path
	} else {
		plan.UnavailableNotes = append(plan.UnavailableNotes, "pdftotext unavailable")
	}
	if command, ok := resolveCommand(policy.ExternalCommand); ok {
		plan.ExternalCommand = command
		plan.ExternalAvailable = true
	} else if strings.TrimSpace(policy.ExternalCommand) != "" {
		plan.UnavailableNotes = append(plan.UnavailableNotes, "external command unavailable")
	}
	if command, ok := resolveCommand(policy.OCRCommand); ok {
		plan.OCRCommand = command
		plan.OCRAvailable = true
	} else if policy.EnableOCR && strings.TrimSpace(policy.OCRCommand) != "" {
		plan.UnavailableNotes = append(plan.UnavailableNotes, "ocr command unavailable")
	}
	plan.Summary = summarizePlan(plan)
	return plan
}

func CurrentPlan() Plan {
	activePlanMu.RLock()
	defer activePlanMu.RUnlock()
	return activePlan
}

func ToolSummaries(plan Plan) []ToolSummary {
	return []ToolSummary{
		{Kind: "pdf_text", Backend: "pdftotext", Available: plan.PDFToTextPath != "", Path: plan.PDFToTextPath},
		{Kind: "external_text", Backend: "external_command", Available: plan.ExternalAvailable, Path: commandName(plan.ExternalCommand)},
		{Kind: "ocr", Backend: "ocr_command", Available: plan.OCRAvailable, Path: commandName(plan.OCRCommand)},
	}
}

func ArtifactObjectKey(sourceKey string, policy config.TextExtractionPolicy) string {
	suffix := strings.TrimSpace(policy.ObjectKeySuffix)
	if suffix == "" {
		suffix = ".text.json"
	}
	return sourceKey + suffix
}

func Generate(ctx context.Context, _ string, contentType string, reader io.Reader, policy config.TextExtractionPolicy) (Result, error) {
	plan := CurrentPlan()
	baseType := baseContentType(contentType)
	limit := policy.MaxInputBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return Result{Status: "failed", ErrorCode: "read_failed"}, err
	}
	if int64(len(body)) > limit {
		return Result{Status: "failed", ErrorCode: "input_too_large"}, fmt.Errorf("text extraction input exceeds %d bytes", limit)
	}

	content := Content{
		Texts:    map[string]string{},
		Sources:  map[string]Source{},
		Metadata: map[string]interface{}{},
	}
	warnings := []string{}
	backend := "direct_text"
	textKey := ""
	text := ""

	switch {
	case isTextLike(baseType):
		textKey = "text"
		text, warnings = normalizeBytes(body, policy.MaxOutputBytes)
	case isOOXML(baseType):
		backend = "ooxml_parser"
		textKey = "extracted"
		text, warnings, err = extractOOXML(body, baseType, policy)
	case baseType == "application/pdf":
		textKey = "extracted"
		text, warnings, backend, err = extractPDFText(ctx, body, policy, plan)
	case strings.HasPrefix(baseType, "image/"):
		backend = "image_metadata"
		if policy.EnableOCR && plan.OCRAvailable {
			ocr, ocrWarnings, ocrErr := runCommand(ctx, plan.OCRCommand, body, policy)
			warnings = append(warnings, ocrWarnings...)
			if ocrErr != nil {
				err = ocrErr
			} else if strings.TrimSpace(ocr) != "" {
				content.Texts["ocr"] = ocr
				content.Sources["ocr"] = Source{Backend: "ocr_command", ContentType: baseType, Warnings: ocrWarnings}
			}
		}
	default:
		if plan.ExternalAvailable {
			backend = "external_command"
			textKey = "extracted"
			text, warnings, err = runCommand(ctx, plan.ExternalCommand, body, policy)
		} else {
			warnings = append(warnings, "unsupported_content_type")
		}
	}
	if err != nil {
		return Result{Content: content, Status: "failed", ErrorCode: extractionErrorCode(err), HasOCR: content.Texts["ocr"] != ""}, err
	}
	if textKey == "extracted" && strings.TrimSpace(text) == "" && plan.ExternalAvailable && backend != "external_command" {
		fallbackText, fallbackWarnings, fallbackErr := runCommand(ctx, plan.ExternalCommand, body, policy)
		if fallbackErr != nil {
			warnings = append(warnings, "external_fallback_failed")
		} else {
			backend = "external_command"
			text = fallbackText
			warnings = append(warnings, fallbackWarnings...)
		}
	}
	if textKey != "" && strings.TrimSpace(text) != "" {
		content.Texts[textKey] = text
		content.Sources[textKey] = Source{Backend: backend, ContentType: baseType, Warnings: warnings}
	}
	if policy.ExtractMetadata {
		metadataTexts, metadata := extractMetadataTexts(body, baseType)
		for key, value := range metadataTexts {
			if strings.TrimSpace(value) != "" {
				content.Texts[key] = normalizeWhitespace(value)
				content.Sources[key] = Source{Backend: "metadata_parser", ContentType: baseType}
			}
		}
		for key, value := range metadata {
			content.Metadata[key] = value
		}
	}
	if len(warnings) > 0 && len(content.Texts) == 0 {
		content.Sources["extraction"] = Source{Backend: backend, ContentType: baseType, Warnings: warnings}
	}
	if len(content.Metadata) == 0 {
		content.Metadata = nil
	}
	if len(content.Sources) == 0 {
		content.Sources = nil
	}
	if len(content.Texts) == 0 {
		content.Texts = nil
	}
	status := "generated"
	if content.Texts == nil && content.Metadata == nil {
		status = "skipped"
	}
	return Result{
		Content:     content,
		Status:      status,
		HasText:     content.Texts != nil && (content.Texts["text"] != "" || content.Texts["extracted"] != ""),
		HasOCR:      content.Texts != nil && content.Texts["ocr"] != "",
		HasMetadata: content.Metadata != nil || (content.Texts != nil && (content.Texts["title"] != "" || content.Texts["description"] != "")),
	}, nil
}

func extractMetadataTexts(body []byte, contentType string) (map[string]string, map[string]interface{}) {
	texts := map[string]string{}
	metadata := map[string]interface{}{}
	switch {
	case isOOXML(contentType):
		ooxmlTexts, ooxmlMetadata := extractOOXMLCoreProperties(body)
		for key, value := range ooxmlTexts {
			texts[key] = value
		}
		for key, value := range ooxmlMetadata {
			metadata[key] = value
		}
	case contentType == "application/pdf":
		for key, value := range extractPDFInfo(body) {
			texts[key] = value
		}
	case contentType == "image/png":
		for key, value := range extractPNGTextMetadata(body) {
			texts[key] = value
		}
	case contentType == "image/jpeg" || contentType == "image/pjpeg":
		for key, value := range extractXMPTextMetadata(body) {
			texts[key] = value
		}
	}
	return texts, metadata
}

func extractOOXMLCoreProperties(body []byte) (map[string]string, map[string]interface{}) {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, nil
	}
	text := extractZipXMLNamedText(reader, "docProps/core.xml")
	texts := map[string]string{}
	metadata := map[string]interface{}{}
	if title := firstXMLValue(text, "title"); title != "" {
		texts["title"] = title
	}
	if description := firstXMLValue(text, "description"); description != "" {
		texts["description"] = description
	} else if subject := firstXMLValue(text, "subject"); subject != "" {
		texts["description"] = subject
	}
	for _, key := range []string{"creator", "keywords", "lastModifiedBy"} {
		if value := firstXMLValue(text, key); value != "" {
			metadata[key] = value
		}
	}
	if len(texts) == 0 {
		texts = nil
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return texts, metadata
}

func extractZipXMLNamedText(reader *zip.Reader, name string) string {
	for _, f := range reader.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		body, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		return string(body)
	}
	return ""
}

func firstXMLValue(body, localName string) string {
	decoder := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != localName {
			continue
		}
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return ""
		}
		return normalizeWhitespace(value)
	}
}

var pdfInfoPattern = regexp.MustCompile(`/(Title|Subject|Keywords|Author)\s*\((?:\\.|[^\\)])*\)`)

func extractPDFInfo(body []byte) map[string]string {
	out := map[string]string{}
	for _, match := range pdfInfoPattern.FindAll(body, 100) {
		parts := bytes.SplitN(match, []byte("("), 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimPrefix(strings.Fields(string(parts[0]))[0], "/")
		value := unescapePDFString(string(parts[1][:len(parts[1])-1]))
		switch key {
		case "Title":
			out["title"] = value
		case "Subject":
			out["description"] = value
		case "Keywords", "Author":
			if out["description"] == "" {
				out["description"] = value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractPNGTextMetadata(body []byte) map[string]string {
	if len(body) < 8 || !bytes.Equal(body[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return nil
	}
	out := map[string]string{}
	for offset := 8; offset+8 <= len(body); {
		length := int(body[offset])<<24 | int(body[offset+1])<<16 | int(body[offset+2])<<8 | int(body[offset+3])
		kind := string(body[offset+4 : offset+8])
		dataStart := offset + 8
		dataEnd := dataStart + length
		if length < 0 || dataEnd+4 > len(body) {
			break
		}
		if kind == "tEXt" {
			parts := bytes.SplitN(body[dataStart:dataEnd], []byte{0}, 2)
			if len(parts) == 2 {
				metadataTextToOutput(out, string(parts[0]), string(parts[1]))
			}
		}
		offset = dataEnd + 4
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractXMPTextMetadata(body []byte) map[string]string {
	start := bytes.Index(body, []byte("<x:xmpmeta"))
	if start < 0 {
		start = bytes.Index(body, []byte("<rdf:RDF"))
	}
	if start < 0 {
		return nil
	}
	end := bytes.Index(body[start:], []byte("</x:xmpmeta>"))
	if end < 0 {
		end = bytes.Index(body[start:], []byte("</rdf:RDF>"))
	}
	if end < 0 {
		return nil
	}
	packet := string(body[start : start+end])
	out := map[string]string{}
	if title := firstXMLValue(packet, "title"); title != "" {
		out["title"] = title
	}
	if description := firstXMLValue(packet, "description"); description != "" {
		out["description"] = description
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func metadataTextToOutput(out map[string]string, key, value string) {
	key = strings.ToLower(strings.TrimSpace(key))
	value = normalizeWhitespace(value)
	if value == "" {
		return
	}
	switch key {
	case "title":
		out["title"] = value
	case "description", "subject", "comment":
		out["description"] = value
	}
}

func Marshal(content Content, policy config.TextExtractionPolicy) ([]byte, error) {
	body, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return nil, err
	}
	limit := policy.MaxOutputBytes
	if limit <= 0 {
		limit = 16 << 20
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("extracted content exceeds %d bytes", limit)
	}
	return body, nil
}

func Filter(content Content, include map[string]bool) Content {
	if len(include) == 0 {
		return content
	}
	out := Content{}
	if include["metadata"] {
		out.Metadata = content.Metadata
	}
	if include["sources"] {
		out.Sources = content.Sources
	}
	for key, value := range content.Texts {
		if include[key] {
			if out.Texts == nil {
				out.Texts = map[string]string{}
			}
			out.Texts[key] = value
		}
	}
	return out
}

func TaskKindsForContentType(contentType string, policy config.TextExtractionPolicy) []string {
	plan := CurrentPlan()
	baseType := baseContentType(contentType)
	var kinds []string
	if isTextLike(baseType) || isOOXML(baseType) || baseType == "application/pdf" || plan.ExternalAvailable {
		kinds = append(kinds, "text_extraction")
	}
	if policy.ExtractMetadata && (strings.HasPrefix(baseType, "image/") || isOOXML(baseType) || baseType == "application/pdf") {
		kinds = append(kinds, "metadata_extraction")
	}
	if policy.EnableOCR && plan.OCRAvailable && (strings.HasPrefix(baseType, "image/") || baseType == "application/pdf") {
		kinds = append(kinds, "ocr_extraction")
	}
	return unique(kinds)
}

func ShouldSchedule(contentType string, policy config.TextExtractionPolicy) bool {
	return policy.Enabled && len(TaskKindsForContentType(contentType, policy)) > 0
}

func baseContentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(contentType)))
	if err == nil {
		return mediaType
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
}

func isTextLike(contentType string) bool {
	return strings.HasPrefix(contentType, "text/") ||
		contentType == "application/json" ||
		contentType == "application/xml" ||
		strings.HasSuffix(contentType, "+json") ||
		strings.HasSuffix(contentType, "+xml") ||
		contentType == "application/javascript" ||
		contentType == "application/x-ndjson"
}

func isOOXML(contentType string) bool {
	switch contentType {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	default:
		return false
	}
}

func normalizeBytes(body []byte, maxOutputBytes int64) (string, []string) {
	warnings := []string{}
	if !utf8.Valid(body) {
		body = bytes.ToValidUTF8(body, []byte("?"))
		warnings = append(warnings, "invalid_utf8_replaced")
	}
	text := normalizeWhitespace(string(body))
	if maxOutputBytes > 0 && int64(len([]byte(text))) > maxOutputBytes {
		text = string([]byte(text)[:maxOutputBytes])
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		warnings = append(warnings, "output_truncated")
	}
	return text, warnings
}

func normalizeWhitespace(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractOOXML(body []byte, contentType string, policy config.TextExtractionPolicy) (string, []string, error) {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", nil, err
	}
	var names []string
	switch contentType {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		names = []string{"word/document.xml"}
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		names = []string{"xl/sharedStrings.xml"}
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		for _, f := range reader.File {
			if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
				names = append(names, f.Name)
			}
		}
		sort.Strings(names)
	}
	var parts []string
	for _, name := range names {
		if text := extractZipXMLText(reader, name); text != "" {
			parts = append(parts, text)
		}
	}
	text, warnings := normalizeBytes([]byte(strings.Join(parts, "\n")), policy.MaxOutputBytes)
	if text == "" {
		warnings = append(warnings, "no_ooxml_text_found")
	}
	return text, warnings, nil
}

func extractZipXMLText(reader *zip.Reader, name string) string {
	for _, f := range reader.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		decoder := xml.NewDecoder(io.LimitReader(rc, 32<<20))
		var parts []string
		for {
			tok, err := decoder.Token()
			if err != nil {
				break
			}
			if charData, ok := tok.(xml.CharData); ok {
				text := strings.TrimSpace(string(charData))
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return normalizeWhitespace(strings.Join(parts, " "))
	}
	return ""
}

var pdfStringPattern = regexp.MustCompile(`\((?:\\.|[^\\)])*\)`)

func extractPDFText(ctx context.Context, body []byte, policy config.TextExtractionPolicy, plan Plan) (string, []string, string, error) {
	text, warnings, err := runPDFToText(ctx, body, policy, plan)
	if plan.PDFToTextPath != "" && err == nil && strings.TrimSpace(text) != "" {
		return text, warnings, "pdftotext", nil
	}
	fallbackText, fallbackWarnings, fallbackErr := extractPDFLiteralText(body, policy.MaxOutputBytes)
	if plan.PDFToTextPath == "" {
		fallbackWarnings = append([]string{"pdftotext_unavailable"}, fallbackWarnings...)
	} else if err != nil {
		fallbackWarnings = append([]string{"pdftotext_unavailable_or_failed"}, fallbackWarnings...)
	}
	warnings = append(warnings, fallbackWarnings...)
	return fallbackText, warnings, "pdf_literal_parser", fallbackErr
}

func runPDFToText(ctx context.Context, body []byte, policy config.TextExtractionPolicy, plan Plan) (string, []string, error) {
	if plan.PDFToTextPath == "" {
		return "", nil, exec.ErrNotFound
	}
	timeout := policy.ExternalTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, plan.PDFToTextPath, "-", "-")
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.Output()
	if cmdCtx.Err() != nil {
		return "", nil, cmdCtx.Err()
	}
	if err != nil {
		return "", nil, err
	}
	text, warnings := normalizeBytes(out, policy.MaxOutputBytes)
	return text, warnings, nil
}

func extractPDFLiteralText(body []byte, maxOutputBytes int64) (string, []string, error) {
	matches := pdfStringPattern.FindAll(body, 10000)
	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		unescaped := unescapePDFString(string(match[1 : len(match)-1]))
		if strings.TrimSpace(unescaped) != "" {
			parts = append(parts, unescaped)
		}
	}
	text, warnings := normalizeBytes([]byte(strings.Join(parts, "\n")), maxOutputBytes)
	if text == "" {
		warnings = append(warnings, "no_literal_pdf_text_found")
	}
	return text, warnings, nil
}

func unescapePDFString(value string) string {
	replacer := strings.NewReplacer(`\(`, "(", `\)`, ")", `\\`, `\`, `\n`, "\n", `\r`, "\n", `\t`, "\t", `\b`, "", `\f`, "")
	return replacer.Replace(value)
}

func runCommand(ctx context.Context, command []string, body []byte, policy config.TextExtractionPolicy) (string, []string, error) {
	if len(command) == 0 {
		return "", nil, errors.New("empty command")
	}
	timeout := policy.ExternalTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.Output()
	if cmdCtx.Err() != nil {
		return "", nil, cmdCtx.Err()
	}
	if err != nil {
		return "", nil, err
	}
	text, warnings := normalizeBytes(out, policy.MaxOutputBytes)
	return text, warnings, nil
}

func resolveCommand(command string) ([]string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, false
	}
	exe := fields[0]
	var resolved string
	if filepath.IsAbs(exe) || strings.ContainsAny(exe, `/\`) {
		if _, err := exec.LookPath(exe); err != nil {
			return nil, false
		}
		resolved = exe
	} else {
		path, err := exec.LookPath(exe)
		if err != nil {
			return nil, false
		}
		resolved = path
	}
	out := append([]string{resolved}, fields[1:]...)
	return out, true
}

func summarizePlan(plan Plan) string {
	if !plan.Enabled {
		return "disabled"
	}
	parts := []string{}
	if plan.PDFToTextPath != "" {
		parts = append(parts, "pdftotext")
	}
	if plan.ExternalAvailable {
		parts = append(parts, "external-command")
	}
	if plan.OCRAvailable {
		parts = append(parts, "ocr-command")
	}
	if len(parts) == 0 {
		return "internal-only"
	}
	return strings.Join(parts, ",")
}

func commandName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return command[0]
}

func extractionErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "extract_failed"
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
