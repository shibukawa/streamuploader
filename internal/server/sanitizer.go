package server

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"image"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"streamuploader/internal/config"
)

func startDocumentFullScan(contentType, originalName string, policy config.SecurityPolicy) (*io.PipeWriter, <-chan error) {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		defer reader.Close()
		limit := policy.ResourceLimits.MaxFileSizeBytes
		if limit <= 0 {
			limit = 1 << 30
		}
		body, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			done <- err
			return
		}
		if int64(len(body)) > limit {
			done <- securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "uploaded file exceeds configured maximum file size"}
			return
		}
		done <- inspectStoredFileObject(bytes.NewReader(body), int64(len(body)), contentType, originalName, policy)
	}()
	return writer, done
}

type sanitizedBody struct {
	reader      io.Reader
	contentType string
}

func applyFileSanitization(reader io.Reader, contentType, originalName string, policy config.SecurityPolicy) (sanitizedBody, error) {
	contentType = normalizeContentType(contentType)
	mode := fileSanitizationMode(contentType, originalName, policy.FileSanitization)
	if !policy.FileSanitization.Enabled || mode == "accept_as_is" {
		return sanitizedBody{reader: reader, contentType: contentType}, nil
	}
	if policy.ResourceLimits.Enabled && policy.ResourceLimits.MaxFileSizeBytes > 0 {
		reader = io.LimitReader(reader, policy.ResourceLimits.MaxFileSizeBytes+1)
	}
	needsFullRead := uploadNeedsFullSanitization(contentType, originalName, mode)
	if !needsFullRead {
		return sanitizedBody{reader: reader, contentType: contentType}, nil
	}
	limit := policy.ResourceLimits.MaxSanitizedMemoryBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	if policy.ResourceLimits.MaxFileSizeBytes > 0 && policy.ResourceLimits.MaxFileSizeBytes < limit {
		limit = policy.ResourceLimits.MaxFileSizeBytes
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return sanitizedBody{}, err
	}
	if int64(len(body)) > limit {
		return sanitizedBody{}, securityUploadError{
			status:  http.StatusRequestEntityTooLarge,
			code:    "resource_limit_exceeded",
			message: "uploaded file exceeds configured in-memory sanitization limit",
		}
	}
	if policy.ResourceLimits.Enabled {
		if err := enforceResourceLimits(body, contentType, originalName, policy.ResourceLimits); err != nil {
			return sanitizedBody{}, err
		}
	}
	if policy.StructuralValidation.Enabled {
		if err := validateStructure(body, contentType, originalName, policy); err != nil {
			return sanitizedBody{}, err
		}
	}
	out := body
	switch {
	case isLegacyOrComplexDocument(contentType, originalName):
		if mode == "accept_as_is" {
			break
		}
		if isLegacyOffice(contentType, originalName) {
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "legacy_office_rejected", message: "legacy Microsoft Office formats are rejected by policy"}
		}
		return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "legacy_document_rejected", message: "legacy or complex rich document formats are rejected by policy"}
	case isSVG(contentType, originalName):
		if mode == "sanitize_when_supported" {
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "SVG sanitization is not supported"}
		}
		if mode == "reject_active_or_external_content" || mode == "secure_default" {
			if err := inspectSVG(body, policy.ResourceLimits, policy.FileSanitization.SVG); err != nil {
				return sanitizedBody{}, err
			}
		}
	case isMarkup(contentType, originalName):
		if mode == "sanitize_when_supported" {
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "markup sanitization is not supported"}
		}
		if mode == "reject_active_or_external_content" || mode == "secure_default" {
			if err := inspectMarkup(body, contentType, originalName, policy.ResourceLimits, policy.FileSanitization.Markup); err != nil {
				return sanitizedBody{}, err
			}
		}
	case isPDF(contentType, originalName):
		if mode == "sanitize_when_supported" {
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "PDF sanitization is not supported"}
		}
		if mode == "reject_active_content" || mode == "secure_default" {
			if err := inspectPDF(body, policy.ResourceLimits); err != nil {
				return sanitizedBody{}, err
			}
		}
	case isOfficeOpenXML(contentType, originalName):
		if mode == "sanitize_when_supported" {
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "Office document sanitization is not supported"}
		}
		if mode == "reject_active_content" || mode == "secure_default" {
			if err := inspectOfficeOpenXML(body, policy.ResourceLimits); err != nil {
				return sanitizedBody{}, err
			}
		}
	case isImage(contentType, originalName):
		switch mode {
		case "sanitize_metadata", "secure_default":
			var err error
			out, err = sanitizeImageMetadata(body, contentType, originalName)
			if err != nil {
				return sanitizedBody{}, err
			}
		case "reject_on_sensitive_metadata":
			if imageHasSensitiveMetadata(body, contentType, originalName) {
				return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sensitive_metadata_detected", message: "uploaded image contains privacy-sensitive metadata"}
			}
		case "sanitize_when_supported":
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "image sanitization mode is unsupported"}
		}
	case isVideo(contentType, originalName):
		switch mode {
		case "sanitize_metadata", "secure_default":
			var err error
			out, err = sanitizeVideoMetadata(body, contentType, originalName)
			if err != nil {
				return sanitizedBody{}, err
			}
		case "sanitize_when_supported":
			return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "video sanitization mode is unsupported"}
		case "reject_on_sensitive_metadata":
			if mediaHasMetadata(body, contentType, originalName) {
				return sanitizedBody{}, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sensitive_metadata_detected", message: "uploaded video contains privacy-sensitive metadata"}
			}
		}
	}
	return sanitizedBody{reader: bytes.NewReader(out), contentType: contentType}, nil
}

func uploadNeedsFullSanitization(contentType, originalName, mode string) bool {
	if mode == "accept_as_is" {
		return false
	}
	return isLegacyOrComplexDocument(contentType, originalName) || isSVG(contentType, originalName) || isMarkup(contentType, originalName) || isImage(contentType, originalName) || isVideo(contentType, originalName)
}

func fileRequiresPostUploadFullScan(contentType, originalName string, policy config.SecurityPolicy) bool {
	if !policy.FileSanitization.Enabled {
		return false
	}
	mode := fileSanitizationMode(contentType, originalName, policy.FileSanitization)
	if mode == "accept_as_is" {
		return false
	}
	return isPDF(contentType, originalName) || isOfficeOpenXML(contentType, originalName)
}

func inspectStoredFileObject(reader io.Reader, size int64, contentType, originalName string, policy config.SecurityPolicy) error {
	limit := policy.ResourceLimits.MaxFileSizeBytes
	if limit <= 0 {
		limit = size
	}
	if limit <= 0 {
		limit = 1 << 30
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "uploaded file exceeds configured maximum file size"}
	}
	if policy.ResourceLimits.Enabled {
		if err := enforceResourceLimits(body, contentType, originalName, policy.ResourceLimits); err != nil {
			return err
		}
	}
	if policy.StructuralValidation.Enabled {
		if err := validateStructure(body, contentType, originalName, policy); err != nil {
			return err
		}
	}
	mode := fileSanitizationMode(contentType, originalName, policy.FileSanitization)
	switch {
	case isPDF(contentType, originalName):
		if mode == "sanitize_when_supported" {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "PDF sanitization is not supported"}
		}
		return inspectPDF(body, policy.ResourceLimits)
	case isOfficeOpenXML(contentType, originalName):
		if mode == "sanitize_when_supported" {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "Office document sanitization is not supported"}
		}
		return inspectOfficeOpenXML(body, policy.ResourceLimits)
	default:
		return nil
	}
}

func fileSanitizationMode(contentType, originalName string, policy config.FileSanitizationPolicy) string {
	if mode := policyModeForKey(contentType, policy); mode != "" {
		return mode
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(originalName)), ".")
	if ext != "" {
		if mode := policyModeForKey(ext, policy); mode != "" {
			return mode
		}
		if mode := policyModeForKey("."+ext, policy); mode != "" {
			return mode
		}
	}
	switch {
	case isSVG(contentType, originalName):
		return fallbackMode(policy.SVG.DefaultMode, "reject_active_or_external_content")
	case isMarkup(contentType, originalName):
		return fallbackMode(policy.Markup.DefaultMode, "reject_active_or_external_content")
	case isImage(contentType, originalName), isVideo(contentType, originalName):
		return fallbackMode(policy.ImageVideoMetadata.DefaultMode, "sanitize_metadata")
	case isPDF(contentType, originalName), isOfficeOpenXML(contentType, originalName):
		return fallbackMode(policy.OfficePDF.DefaultMode, "reject_active_content")
	case isLegacyOrComplexDocument(contentType, originalName):
		return fallbackMode(policy.LegacyOrComplexDocuments.DefaultMode, "reject")
	default:
		return fallbackMode(policy.DefaultMode, "secure_default")
	}
}

func policyModeForKey(key string, policy config.FileSanitizationPolicy) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || policy.PerFileType == nil {
		return ""
	}
	if entry, ok := policy.PerFileType[key]; ok {
		return strings.ToLower(strings.TrimSpace(entry.Mode))
	}
	return ""
}

func fallbackMode(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func enforceResourceLimits(body []byte, contentType, originalName string, policy config.ResourceLimitPolicy) error {
	if policy.MaxFileSizeBytes > 0 && int64(len(body)) > policy.MaxFileSizeBytes {
		return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "uploaded file exceeds configured maximum file size"}
	}
	if isImage(contentType, originalName) && !isSVG(contentType, originalName) {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "image structure could not be parsed"}
		}
		if policy.MaxImageWidth > 0 && cfg.Width > policy.MaxImageWidth {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "image width exceeds configured limit"}
		}
		if policy.MaxImageHeight > 0 && cfg.Height > policy.MaxImageHeight {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "image height exceeds configured limit"}
		}
		if policy.MaxImagePixelCount > 0 && int64(cfg.Width)*int64(cfg.Height) > policy.MaxImagePixelCount {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "image pixel count exceeds configured limit"}
		}
	}
	return nil
}

func validateStructure(body []byte, contentType, originalName string, policy config.SecurityPolicy) error {
	switch {
	case isPNG(contentType, originalName):
		return validateAndSanitizePNG(body, false)
	case isJPEG(contentType, originalName):
		if !bytes.HasPrefix(body, []byte{0xff, 0xd8}) || !bytes.HasSuffix(bytes.TrimSpace(body), []byte{0xff, 0xd9}) {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "JPEG marker structure is invalid"}
		}
	case isPDF(contentType, originalName):
		if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("%PDF-")) {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PDF header is invalid"}
		}
	case isOfficeOpenXML(contentType, originalName):
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "Office package ZIP structure is invalid"}
		}
		if policy.ResourceLimits.MaxZIPEntries > 0 && int64(len(zr.File)) > policy.ResourceLimits.MaxZIPEntries {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "Office package has too many ZIP entries"}
		}
	}
	return nil
}

func sanitizeImageMetadata(body []byte, contentType, originalName string) ([]byte, error) {
	switch {
	case isJPEG(contentType, originalName):
		return sanitizeJPEGMetadata(body)
	case isPNG(contentType, originalName):
		if err := validateAndSanitizePNG(body, false); err != nil {
			return nil, err
		}
		return sanitizePNGMetadata(body)
	case isWEBP(contentType, originalName):
		return sanitizeWEBPMetadata(body)
	default:
		if imageHasSensitiveMetadata(body, contentType, originalName) {
			return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "metadata sanitizer is not available for this image format"}
		}
		return body, nil
	}
}

func imageHasSensitiveMetadata(body []byte, contentType, originalName string) bool {
	switch {
	case isJPEG(contentType, originalName):
		return jpegHasAPP1(body)
	case isPNG(contentType, originalName):
		return pngHasSensitiveMetadata(body)
	case isWEBP(contentType, originalName):
		return webpHasSensitiveMetadata(body)
	default:
		lower := bytes.ToLower(body)
		return bytes.Contains(lower, []byte("exif")) || bytes.Contains(lower, []byte("xmp"))
	}
}

func mediaHasMetadata(body []byte, contentType, originalName string) bool {
	if !isVideo(contentType, originalName) {
		return false
	}
	lower := bytes.ToLower(body)
	for _, marker := range [][]byte{[]byte("exif"), []byte("xmp"), []byte("\xa9day"), []byte("\xa9mod"), []byte("\xa9xyz"), []byte("gps"), []byte("udta"), []byte("meta")} {
		if bytes.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func sanitizeVideoMetadata(body []byte, contentType, originalName string) ([]byte, error) {
	if !isBMFFVideo(contentType, originalName) {
		if mediaHasMetadata(body, contentType, originalName) {
			return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "sanitizer_unavailable", message: "metadata sanitizer is not available for this video format"}
		}
		return body, nil
	}
	out, changed, err := sanitizeBMFFBoxes(body, 0)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return out, nil
}

func isBMFFVideo(contentType, originalName string) bool {
	switch contentType {
	case "video/mp4", "video/quicktime":
		return true
	default:
		return hasAnyExtension(originalName, ".mp4", ".m4v", ".mov")
	}
}

func sanitizeBMFFBoxes(data []byte, depth int) ([]byte, bool, error) {
	if depth > 16 {
		return nil, false, securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "media container nesting exceeds configured limit"}
	}
	var out bytes.Buffer
	changed := false
	for i := 0; i < len(data); {
		if len(data)-i < 8 {
			return nil, false, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "media box header is truncated"}
		}
		size := uint64(binary.BigEndian.Uint32(data[i : i+4]))
		boxType := string(data[i+4 : i+8])
		headerSize := 8
		if size == 1 {
			if len(data)-i < 16 {
				return nil, false, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "media large-size box header is truncated"}
			}
			size = binary.BigEndian.Uint64(data[i+8 : i+16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(len(data) - i)
		}
		if size < uint64(headerSize) || uint64(i)+size > uint64(len(data)) {
			return nil, false, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "media box size is invalid"}
		}
		box := data[i : i+int(size)]
		payload := box[headerSize:]
		if bmffMetadataBox(boxType, payload) {
			changed = true
			i += int(size)
			continue
		}
		if bmffContainerBox(boxType) {
			childPayload := payload
			prefix := []byte(nil)
			if boxType == "meta" && len(payload) >= 4 {
				prefix = payload[:4]
				childPayload = payload[4:]
			}
			sanitizedChildren, childChanged, err := sanitizeBMFFBoxes(childPayload, depth+1)
			if err != nil {
				return nil, false, err
			}
			if childChanged {
				changed = true
				newPayload := append(append([]byte(nil), prefix...), sanitizedChildren...)
				writeBMFFBox(&out, boxType, newPayload)
			} else {
				out.Write(box)
			}
		} else {
			out.Write(box)
		}
		i += int(size)
	}
	return out.Bytes(), changed, nil
}

func bmffMetadataBox(boxType string, payload []byte) bool {
	switch boxType {
	case "udta", "meta", "ilst", "keys", "Exif", "XMP_", "\xa9day", "\xa9mod", "\xa9xyz":
		return true
	case "uuid":
		lower := bytes.ToLower(payload)
		return bytes.Contains(lower, []byte("xmp")) || bytes.Contains(lower, []byte("exif"))
	default:
		return false
	}
}

func bmffContainerBox(boxType string) bool {
	switch boxType {
	case "moov", "trak", "mdia", "minf", "stbl", "edts", "dinf", "moof", "traf":
		return true
	default:
		return false
	}
}

func writeBMFFBox(out *bytes.Buffer, boxType string, payload []byte) {
	size := uint64(8 + len(payload))
	if size <= uint64(^uint32(0)) {
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], uint32(size))
		copy(header[4:8], []byte(boxType))
		out.Write(header[:])
		out.Write(payload)
		return
	}
	var header [16]byte
	binary.BigEndian.PutUint32(header[:4], 1)
	copy(header[4:8], []byte(boxType))
	binary.BigEndian.PutUint64(header[8:16], size)
	out.Write(header[:])
	out.Write(payload)
}

func sanitizeJPEGMetadata(body []byte) ([]byte, error) {
	if len(body) < 4 || body[0] != 0xff || body[1] != 0xd8 {
		return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "JPEG marker structure is invalid"}
	}
	out := append([]byte{}, body[:2]...)
	i := 2
	for i < len(body) {
		if body[i] != 0xff {
			out = append(out, body[i:]...)
			return out, nil
		}
		for i < len(body) && body[i] == 0xff {
			i++
		}
		if i >= len(body) {
			break
		}
		marker := body[i]
		i++
		if marker == 0xd9 || marker == 0xda {
			out = append(out, 0xff, marker)
			out = append(out, body[i:]...)
			return out, nil
		}
		if i+2 > len(body) {
			return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "JPEG segment length is invalid"}
		}
		segLen := int(binary.BigEndian.Uint16(body[i : i+2]))
		if segLen < 2 || i+segLen > len(body) {
			return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "JPEG segment length is invalid"}
		}
		segment := body[i : i+segLen]
		if marker != 0xe1 {
			out = append(out, 0xff, marker)
			out = append(out, segment...)
		}
		i += segLen
	}
	return out, nil
}

func jpegHasAPP1(body []byte) bool {
	i := 2
	if len(body) < 4 || body[0] != 0xff || body[1] != 0xd8 {
		return false
	}
	for i+4 <= len(body) {
		if body[i] != 0xff {
			return false
		}
		i++
		marker := body[i]
		i++
		if marker == 0xd9 || marker == 0xda {
			return false
		}
		if i+2 > len(body) {
			return false
		}
		segLen := int(binary.BigEndian.Uint16(body[i : i+2]))
		if segLen < 2 || i+segLen > len(body) {
			return false
		}
		if marker == 0xe1 {
			return true
		}
		i += segLen
	}
	return false
}

func validateAndSanitizePNG(body []byte, sanitize bool) error {
	if len(body) < 12 || !bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG signature is invalid"}
	}
	seenIHDR := false
	for i := 8; i < len(body); {
		if i+12 > len(body) {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG chunk is truncated"}
		}
		length := int(binary.BigEndian.Uint32(body[i : i+4]))
		chunkType := body[i+4 : i+8]
		dataStart := i + 8
		dataEnd := dataStart + length
		crcEnd := dataEnd + 4
		if length < 0 || crcEnd > len(body) {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG chunk length is invalid"}
		}
		want := binary.BigEndian.Uint32(body[dataEnd:crcEnd])
		got := crc32.ChecksumIEEE(body[i+4 : dataEnd])
		if got != want {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG chunk CRC is invalid"}
		}
		name := string(chunkType)
		if name == "IHDR" {
			seenIHDR = true
		}
		if name == "IEND" {
			if !seenIHDR {
				return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG IHDR chunk is missing"}
			}
			if crcEnd != len(body) {
				return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG has trailing data after IEND"}
			}
			return nil
		}
		i = crcEnd
		_ = sanitize
	}
	return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "PNG IEND chunk is missing"}
}

func sanitizePNGMetadata(body []byte) ([]byte, error) {
	if err := validateAndSanitizePNG(body, true); err != nil {
		return nil, err
	}
	out := append([]byte{}, body[:8]...)
	for i := 8; i < len(body); {
		length := int(binary.BigEndian.Uint32(body[i : i+4]))
		chunkType := string(body[i+4 : i+8])
		end := i + 12 + length
		if !pngMetadataChunk(chunkType) {
			out = append(out, body[i:end]...)
		}
		i = end
	}
	return out, nil
}

func pngHasSensitiveMetadata(body []byte) bool {
	if err := validateAndSanitizePNG(body, false); err != nil {
		return false
	}
	for i := 8; i < len(body); {
		length := int(binary.BigEndian.Uint32(body[i : i+4]))
		chunkType := string(body[i+4 : i+8])
		if pngMetadataChunk(chunkType) {
			return true
		}
		i += 12 + length
	}
	return false
}

func pngMetadataChunk(chunkType string) bool {
	switch chunkType {
	case "eXIf", "tEXt", "zTXt", "iTXt", "tIME":
		return true
	default:
		return false
	}
}

func sanitizeWEBPMetadata(body []byte) ([]byte, error) {
	if len(body) < 12 || string(body[:4]) != "RIFF" || string(body[8:12]) != "WEBP" {
		return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "WebP RIFF structure is invalid"}
	}
	var chunks bytes.Buffer
	changed := false
	for i := 12; i < len(body); {
		if i+8 > len(body) {
			return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "WebP chunk header is truncated"}
		}
		chunkType := string(body[i : i+4])
		chunkSize := int(binary.LittleEndian.Uint32(body[i+4 : i+8]))
		chunkEnd := i + 8 + chunkSize
		paddedEnd := chunkEnd
		if chunkSize%2 == 1 {
			paddedEnd++
		}
		if chunkSize < 0 || chunkEnd > len(body) || paddedEnd > len(body) {
			return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "WebP chunk size is invalid"}
		}
		if chunkType == "EXIF" || chunkType == "XMP " {
			changed = true
		} else {
			chunks.Write(body[i:paddedEnd])
		}
		i = paddedEnd
	}
	if !changed {
		return body, nil
	}
	out := make([]byte, 12)
	copy(out[:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(out[4:8], uint32(4+chunks.Len()))
	copy(out[8:12], []byte("WEBP"))
	out = append(out, chunks.Bytes()...)
	return out, nil
}

func webpHasSensitiveMetadata(body []byte) bool {
	if len(body) < 12 || string(body[:4]) != "RIFF" || string(body[8:12]) != "WEBP" {
		return false
	}
	for i := 12; i+8 <= len(body); {
		chunkType := string(body[i : i+4])
		chunkSize := int(binary.LittleEndian.Uint32(body[i+4 : i+8]))
		chunkEnd := i + 8 + chunkSize
		paddedEnd := chunkEnd
		if chunkSize%2 == 1 {
			paddedEnd++
		}
		if chunkSize < 0 || chunkEnd > len(body) || paddedEnd > len(body) {
			return false
		}
		if chunkType == "EXIF" || chunkType == "XMP " {
			return true
		}
		i = paddedEnd
	}
	return false
}

func inspectSVG(body []byte, limits config.ResourceLimitPolicy, policy config.SVGSanitizationPolicy) error {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth := 0
	objects := int64(0)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "SVG XML is malformed"}
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			objects++
			if limits.MaxXMLDepth > 0 && depth > limits.MaxXMLDepth {
				return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "SVG XML depth exceeds configured limit"}
			}
			if limits.MaxObjectCount > 0 && objects > limits.MaxObjectCount {
				return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "SVG object count exceeds configured limit"}
			}
			name := strings.ToLower(t.Name.Local)
			switch name {
			case "script":
				return svgReject("svg_script_detected", "SVG script element is not allowed")
			case "foreignobject", "iframe", "object", "embed":
				return svgReject("svg_active_content_rejected", "SVG active element is not allowed")
			case "image", "audio", "video", "use":
				if svgElementHasExternalRef(t, policy) {
					return svgReject("svg_external_reference_detected", "SVG external reference is not allowed")
				}
			}
			for _, attr := range t.Attr {
				if svgUnsafeAttribute(attr, policy) {
					return svgReject("svg_active_content_rejected", "SVG active or external attribute is not allowed")
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}

func inspectMarkup(body []byte, contentType, originalName string, limits config.ResourceLimitPolicy, policy config.MarkupSanitizationPolicy) error {
	if isXML(contentType, originalName) {
		return inspectXMLMarkup(body, limits)
	}
	if isMarkdown(contentType, originalName) && !policy.MarkdownRawHTMLInspection {
		return nil
	}
	if isHTML(contentType, originalName) && !policy.HTMLActiveContentInspection {
		return nil
	}
	return inspectHTMLLikeMarkup(body, limits)
}

func inspectHTMLLikeMarkup(body []byte, limits config.ResourceLimitPolicy) error {
	lower := bytes.ToLower(body)
	objectCount := int64(bytes.Count(lower, []byte("<")))
	if limits.MaxObjectCount > 0 && objectCount > limits.MaxObjectCount {
		return markupReject(http.StatusRequestEntityTooLarge, "markup_parser_limit_exceeded", "markup object count exceeds configured limit")
	}
	if containsAnyBytes(lower, [][]byte{
		[]byte("<script"),
		[]byte("<iframe"),
	}) {
		if bytes.Contains(lower, []byte("<script")) {
			return markupReject(http.StatusUnsupportedMediaType, "markup_script_detected", "markup script element is not allowed")
		}
		return markupReject(http.StatusUnsupportedMediaType, "markup_iframe_detected", "markup iframe element is not allowed")
	}
	if containsAnyBytes(lower, [][]byte{
		[]byte("<object"), []byte("<embed"), []byte("<applet"), []byte("<frame"), []byte("<frameset"), []byte("<form"),
	}) {
		return markupReject(http.StatusUnsupportedMediaType, "markup_active_content_rejected", "markup active or embedded element is not allowed")
	}
	if bytes.Contains(lower, []byte("http-equiv")) && bytes.Contains(lower, []byte("refresh")) {
		return markupReject(http.StatusUnsupportedMediaType, "markup_external_reference_detected", "markup meta refresh is not allowed")
	}
	if containsAnyBytes(lower, [][]byte{
		[]byte(" onclick="), []byte(" onload="), []byte(" onerror="), []byte(" onmouseover="), []byte(" onfocus="), []byte(" onsubmit="),
	}) || containsEventHandlerAttribute(lower) {
		return markupReject(http.StatusUnsupportedMediaType, "markup_event_handler_detected", "markup event handler attribute is not allowed")
	}
	if bytes.Contains(lower, []byte("javascript:")) {
		return markupReject(http.StatusUnsupportedMediaType, "markup_javascript_url_detected", "markup JavaScript URL is not allowed")
	}
	if containsAnyBytes(lower, [][]byte{
		[]byte("<link"), []byte("@import"), []byte("url(http:"), []byte("url(https:"), []byte("url(//"), []byte(" src=\"http:"), []byte(" src='http:"), []byte(" src=\"https:"), []byte(" src='https:"),
	}) {
		return markupReject(http.StatusUnsupportedMediaType, "markup_external_reference_detected", "markup external reference is not allowed")
	}
	return nil
}

func containsEventHandlerAttribute(lower []byte) bool {
	for i := 0; i+3 < len(lower); i++ {
		if lower[i] != ' ' && lower[i] != '\n' && lower[i] != '\r' && lower[i] != '\t' {
			continue
		}
		if lower[i+1] != 'o' || lower[i+2] != 'n' {
			continue
		}
		j := i + 3
		for ; j < len(lower); j++ {
			c := lower[j]
			if (c >= 'a' && c <= 'z') || c == '-' || c == ':' {
				continue
			}
			break
		}
		if j > i+3 && j < len(lower) && lower[j] == '=' {
			return true
		}
	}
	return false
}

func inspectXMLMarkup(body []byte, limits config.ResourceLimitPolicy) error {
	lower := bytes.ToLower(body)
	if bytes.Contains(lower, []byte("<!doctype")) {
		if containsAnyBytes(lower, [][]byte{[]byte("system"), []byte("public"), []byte("<!entity")}) {
			return markupReject(http.StatusUnsupportedMediaType, "xml_external_entity_detected", "XML external entity or DTD subset is not allowed")
		}
	}
	if bytes.Contains(lower, []byte("<!entity")) {
		return markupReject(http.StatusUnsupportedMediaType, "xml_external_entity_detected", "XML entity declaration is not allowed")
	}
	if bytes.Contains(lower, []byte("<xi:include")) || bytes.Contains(lower, []byte("<xinclude:include")) {
		return markupReject(http.StatusUnsupportedMediaType, "xml_xinclude_detected", "XML XInclude is not allowed")
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth := 0
	objects := int64(0)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "XML is malformed"}
		}
		switch t := token.(type) {
		case xml.Directive:
			if bytes.Contains(bytes.ToLower([]byte(t)), []byte("doctype")) || bytes.Contains(bytes.ToLower([]byte(t)), []byte("entity")) {
				return markupReject(http.StatusUnsupportedMediaType, "xml_external_entity_detected", "XML entity declaration is not allowed")
			}
		case xml.StartElement:
			depth++
			objects++
			if limits.MaxXMLDepth > 0 && depth > limits.MaxXMLDepth {
				return markupReject(http.StatusRequestEntityTooLarge, "markup_parser_limit_exceeded", "XML depth exceeds configured limit")
			}
			if limits.MaxObjectCount > 0 && objects > limits.MaxObjectCount {
				return markupReject(http.StatusRequestEntityTooLarge, "markup_parser_limit_exceeded", "XML object count exceeds configured limit")
			}
			name := strings.ToLower(t.Name.Local)
			space := strings.ToLower(t.Name.Space)
			if name == "include" && (space == "http://www.w3.org/2001/xinclude" || strings.EqualFold(t.Name.Space, "xi")) {
				return markupReject(http.StatusUnsupportedMediaType, "xml_xinclude_detected", "XML XInclude is not allowed")
			}
		case xml.EndElement:
			depth--
		}
	}
}

func containsAnyBytes(body []byte, needles [][]byte) bool {
	for _, needle := range needles {
		if bytes.Contains(body, needle) {
			return true
		}
	}
	return false
}

func markupReject(status int, code, message string) error {
	return securityUploadError{status: status, code: code, message: message}
}

func svgElementHasExternalRef(el xml.StartElement, policy config.SVGSanitizationPolicy) bool {
	for _, attr := range el.Attr {
		if strings.EqualFold(attr.Name.Local, "href") && svgExternalReference(attr.Value, policy) {
			return true
		}
	}
	return false
}

func svgUnsafeAttribute(attr xml.Attr, policy config.SVGSanitizationPolicy) bool {
	name := strings.ToLower(attr.Name.Local)
	value := strings.TrimSpace(strings.ToLower(attr.Value))
	if strings.HasPrefix(name, "on") {
		return true
	}
	switch name {
	case "href", "src":
		return svgExternalReference(value, policy)
	case "style":
		return strings.Contains(value, "url(") || strings.Contains(value, "@import")
	default:
		return strings.Contains(value, "javascript:")
	}
}

func svgExternalReference(value string, policy config.SVGSanitizationPolicy) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || strings.HasPrefix(value, "#") {
		return false
	}
	if strings.HasPrefix(value, "data:") {
		return !policy.AllowDataURLs
	}
	return strings.HasPrefix(value, "http:") || strings.HasPrefix(value, "https:") || strings.HasPrefix(value, "//") || strings.HasPrefix(value, "file:")
}

func svgReject(code, message string) error {
	return securityUploadError{status: http.StatusUnsupportedMediaType, code: code, message: message}
}

func inspectPDF(body []byte, limits config.ResourceLimitPolicy) error {
	lower := bytes.ToLower(body)
	rejectTokens := map[string]string{
		"/encrypt":      "PDF encryption is not allowed",
		"/javascript":   "PDF JavaScript is not allowed",
		"/js":           "PDF JavaScript action is not allowed",
		"/launch":       "PDF Launch action is not allowed",
		"/openaction":   "PDF OpenAction is not allowed",
		"/aa":           "PDF additional actions are not allowed",
		"/embeddedfile": "PDF embedded files are not allowed",
		"/richmedia":    "PDF rich media is not allowed",
		"/3d":           "PDF 3D content is not allowed",
		"/xfa":          "PDF XFA forms are not allowed",
		"/uri":          "PDF external links are not allowed",
	}
	for token, message := range rejectTokens {
		if bytes.Contains(lower, []byte(token)) {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "document_active_content_rejected", message: message}
		}
	}
	if limits.MaxPDFPageCount > 0 {
		pages := bytes.Count(lower, []byte("/type/page"))
		if int64(pages) > limits.MaxPDFPageCount {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "PDF page count exceeds configured limit"}
		}
	}
	if limits.MaxObjectCount > 0 {
		objects := bytes.Count(lower, []byte(" obj"))
		if int64(objects) > limits.MaxObjectCount {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "PDF object count exceeds configured limit"}
		}
	}
	return nil
}

func inspectOfficeOpenXML(body []byte, limits config.ResourceLimitPolicy) error {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "Office package ZIP structure is invalid"}
	}
	if limits.MaxZIPEntries > 0 && int64(len(zr.File)) > limits.MaxZIPEntries {
		return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "Office package has too many ZIP entries"}
	}
	for _, entry := range zr.File {
		name := strings.ToLower(entry.Name)
		if limits.MaxDecompressedSizeBytes > 0 && int64(entry.UncompressedSize64) > limits.MaxDecompressedSizeBytes {
			return securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "Office package part exceeds decompressed size limit"}
		}
		if officeDangerousPart(name) {
			return securityUploadError{status: http.StatusUnsupportedMediaType, code: "document_active_content_rejected", message: fmt.Sprintf("Office document contains dangerous part %s", entry.Name)}
		}
		if strings.HasSuffix(name, ".rels") || strings.HasSuffix(name, ".xml") {
			data, err := readZipEntry(entry, limits.MaxDecompressedSizeBytes)
			if err != nil {
				return err
			}
			if officeXMLHasDangerousContent(data) {
				return securityUploadError{status: http.StatusUnsupportedMediaType, code: "document_active_content_rejected", message: fmt.Sprintf("Office document contains dangerous relationship or active content in %s", entry.Name)}
			}
		}
	}
	return nil
}

func readZipEntry(entry *zip.File, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, securityUploadError{status: http.StatusUnsupportedMediaType, code: "structural_validation_failed", message: "Office package part could not be opened"}
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, securityUploadError{status: http.StatusRequestEntityTooLarge, code: "resource_limit_exceeded", message: "Office package part exceeds decompressed size limit"}
	}
	return data, nil
}

func officeDangerousPart(name string) bool {
	dangerousFragments := []string{
		"vbaproject.bin", "/activex/", "/embeddings/", "oleobject", "externalLinks/", "printersettings/",
	}
	for _, fragment := range dangerousFragments {
		if strings.Contains(name, strings.ToLower(fragment)) {
			return true
		}
	}
	return false
}

func officeXMLHasDangerousContent(data []byte) bool {
	lower := bytes.ToLower(data)
	for _, marker := range [][]byte{
		[]byte(`targetmode="external"`),
		[]byte("http://"),
		[]byte("https://"),
		[]byte("oleobject"),
		[]byte("activex"),
		[]byte("vba"),
		[]byte("externalLink"),
		[]byte("attachedTemplate"),
	} {
		if bytes.Contains(lower, bytes.ToLower(marker)) {
			return true
		}
	}
	return false
}

func isImage(contentType, originalName string) bool {
	return strings.HasPrefix(contentType, "image/") || hasAnyExtension(originalName, ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".tif", ".tiff", ".bmp", ".svg")
}

func isVideo(contentType, originalName string) bool {
	return strings.HasPrefix(contentType, "video/") || hasAnyExtension(originalName, ".mp4", ".mov", ".m4v", ".avi", ".mkv", ".webm")
}

func isJPEG(contentType, originalName string) bool {
	return contentType == "image/jpeg" || contentType == "image/pjpeg" || hasAnyExtension(originalName, ".jpg", ".jpeg")
}

func isPNG(contentType, originalName string) bool {
	return contentType == "image/png" || hasAnyExtension(originalName, ".png")
}

func isWEBP(contentType, originalName string) bool {
	return contentType == "image/webp" || hasAnyExtension(originalName, ".webp")
}

func isSVG(contentType, originalName string) bool {
	return contentType == "image/svg+xml" || hasAnyExtension(originalName, ".svg")
}

func isPDF(contentType, originalName string) bool {
	return contentType == "application/pdf" || hasAnyExtension(originalName, ".pdf")
}

func isOfficeOpenXML(contentType, originalName string) bool {
	switch contentType {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	default:
		return hasAnyExtension(originalName, ".docx", ".xlsx", ".pptx")
	}
}

func isLegacyOffice(contentType, originalName string) bool {
	switch contentType {
	case "application/msword", "application/vnd.ms-excel", "application/vnd.ms-powerpoint":
		return true
	default:
		return hasAnyExtension(originalName, ".doc", ".xls", ".ppt")
	}
}

func isLegacyOrComplexDocument(contentType, originalName string) bool {
	return isLegacyOffice(contentType, originalName) || isRTF(contentType, originalName)
}

func isRTF(contentType, originalName string) bool {
	switch contentType {
	case "application/rtf", "text/rtf", "application/x-rtf", "text/richtext":
		return true
	default:
		return hasAnyExtension(originalName, ".rtf")
	}
}

func isMarkup(contentType, originalName string) bool {
	return isMarkdown(contentType, originalName) || isHTML(contentType, originalName) || isXML(contentType, originalName)
}

func isMarkdown(contentType, originalName string) bool {
	return contentType == "text/markdown" || hasAnyExtension(originalName, ".md", ".markdown")
}

func isHTML(contentType, originalName string) bool {
	switch contentType {
	case "text/html", "application/xhtml+xml":
		return true
	default:
		return hasAnyExtension(originalName, ".html", ".htm", ".xhtml")
	}
}

func isXML(contentType, originalName string) bool {
	return contentType == "application/xml" || contentType == "text/xml" || strings.HasSuffix(contentType, "+xml") || hasAnyExtension(originalName, ".xml")
}

func hasAnyExtension(name string, extensions ...string) bool {
	ext := strings.ToLower(path.Ext(name))
	for _, candidate := range extensions {
		if ext == candidate {
			return true
		}
	}
	return false
}

func parseIntAttr(value string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return n
}
