package server

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
	"github.com/xi2/xz"

	"streamuploader/internal/config"
	"streamuploader/internal/storage"
)

type archiveKind string

const (
	archiveUnknown archiveKind = ""
	archiveZip     archiveKind = "zip"
	archiveGzip    archiveKind = "gzip"
	archiveTar     archiveKind = "tar"
	archiveZstd    archiveKind = "zstd"
	archiveBrotli  archiveKind = "brotli"
	archiveBzip2   archiveKind = "bzip2"
	archiveXZ      archiveKind = "xz"
	archive7z      archiveKind = "7z"
)

func shouldInspectArchive(prefix []byte, contentType, originalName string) bool {
	if archiveKindFor(contentType, originalName) != archiveUnknown {
		return true
	}
	return archiveKindFromMagic(prefix) != archiveUnknown
}

func inspectArchiveObject(ctx context.Context, store storage.Store, bucket, objectKey string, compressedSize int64, kind archiveKind, policy config.ArchiveGuardPolicy) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(policy.MaxInspectionTimeMS)*time.Millisecond)
	defer cancel()
	switch kind {
	case archiveZip:
		return inspectZipArchive(ctx, s3ObjectReaderAt{ctx: ctx, store: store, bucket: bucket, key: objectKey}, compressedSize, policy)
	case archiveGzip:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, policy, inspectGzipArchive)
	case archiveTar:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, policy, func(ctx context.Context, reader io.Reader, _ int64, policy config.ArchiveGuardPolicy) error {
			return inspectTarArchive(ctx, reader, policy)
		})
	case archiveZstd:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, policy, inspectZstdArchive)
	case archiveBrotli:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, policy, inspectBrotliArchive)
	case archiveBzip2:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, policy, inspectBzip2Archive)
	case archiveXZ:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, policy, inspectXZArchive)
	case archive7z:
		return inspect7zArchive(ctx, s3ObjectReaderAt{ctx: ctx, store: store, bucket: bucket, key: objectKey}, compressedSize, policy)
	default:
		if policy.Strict {
			return archiveSecurityError("archive_unsupported_method", "archive type could not be inspected")
		}
		return nil
	}
}

type streamArchiveInspector func(context.Context, io.Reader, int64, config.ArchiveGuardPolicy) error

func inspectStreamArchiveObject(ctx context.Context, store storage.Store, bucket, objectKey string, compressedSize int64, policy config.ArchiveGuardPolicy, inspect streamArchiveInspector) error {
	out, err := store.GetObject(ctx, storage.GetInput{Bucket: bucket, Key: objectKey})
	if err != nil {
		return err
	}
	defer out.Body.Close()
	return inspect(ctx, out.Body, compressedSize, policy)
}

type s3ObjectReaderAt struct {
	ctx    context.Context
	store  storage.Store
	bucket string
	key    string
}

func (r s3ObjectReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	end := off + int64(len(p)) - 1
	out, err := r.store.GetObject(r.ctx, storage.GetInput{
		Bucket: r.bucket,
		Key:    r.key,
		Range:  fmt.Sprintf("bytes=%d-%d", off, end),
	})
	if err != nil {
		return 0, err
	}
	defer out.Body.Close()
	n, readErr := io.ReadFull(out.Body, p)
	if errors.Is(readErr, io.ErrUnexpectedEOF) {
		readErr = io.EOF
	}
	return n, readErr
}

func inspectZipArchive(ctx context.Context, readerAt io.ReaderAt, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	zr, err := zip.NewReader(readerAt, compressedSize)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "zip archive could not be parsed")
	}
	if int64(len(zr.File)) > policy.MaxEntries {
		return archiveSecurityError("archive_too_many_entries", "archive has too many entries")
	}
	total := int64(0)
	seen := map[string]struct{}{}
	for _, entry := range zr.File {
		if err := ctx.Err(); err != nil {
			return archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
		}
		name := strings.TrimSpace(entry.Name)
		if len(name) > int(policy.MaxFilenameBytes) {
			return archiveSecurityError("archive_path_unsafe", "archive entry name is too long")
		}
		cleanName, err := safeArchivePath(name)
		if err != nil {
			return archiveSecurityError("archive_path_unsafe", err.Error())
		}
		if _, ok := seen[cleanName]; ok {
			return archiveSecurityError("archive_path_unsafe", "archive has duplicate entry path")
		}
		seen[cleanName] = struct{}{}
		if entry.Flags&0x1 != 0 && !policy.AllowEncrypted {
			return archiveSecurityError("archive_unsupported_method", "encrypted archive entries are not allowed")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if archiveEntryIsLink(entry.FileInfo()) {
			return archiveSecurityError("archive_link_rejected", "archive links are not allowed")
		}
		size := int64(entry.UncompressedSize64)
		if size > policy.MaxSingleEntryBytes {
			return archiveSecurityError("archive_too_large", "archive entry exceeds max single entry bytes")
		}
		total += size
		if total > policy.MaxTotalUncompressedBytes {
			return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
		}
		if compressedSize > 0 && float64(total)/float64(compressedSize) > policy.MaxCompressionRatio {
			return archiveSecurityError("archive_ratio_exceeded", "archive compression ratio exceeds limit")
		}
	}
	return nil
}

func inspectGzipArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "gzip stream could not be parsed")
	}
	defer gz.Close()
	return countArchiveStream(ctx, gz, compressedSize, policy)
}

func inspectZstdArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	zr, err := zstd.NewReader(reader)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "zstd stream could not be parsed")
	}
	defer zr.Close()
	return countArchiveStream(ctx, zr, compressedSize, policy)
}

func inspectBrotliArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return countArchiveStream(ctx, brotli.NewReader(reader), compressedSize, policy)
}

func inspectBzip2Archive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return countArchiveStream(ctx, bzip2.NewReader(reader), compressedSize, policy)
}

func inspectXZArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	dictMax := uint32(policy.WorkerMemoryBytes)
	if policy.WorkerMemoryBytes <= 0 || policy.WorkerMemoryBytes > int64(^uint32(0)) {
		dictMax = xz.DefaultDictMax
	}
	xr, err := xz.NewReader(reader, dictMax)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "xz stream could not be parsed")
	}
	return countArchiveStream(ctx, xr, compressedSize, policy)
}

func inspect7zArchive(ctx context.Context, readerAt io.ReaderAt, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	szr, err := sevenzip.NewReader(readerAt, compressedSize)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "7z archive could not be parsed")
	}
	if int64(len(szr.File)) > policy.MaxEntries {
		return archiveSecurityError("archive_too_many_entries", "archive has too many entries")
	}
	total := int64(0)
	seen := map[string]struct{}{}
	for _, entry := range szr.File {
		if err := ctx.Err(); err != nil {
			return archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
		}
		if len(entry.Name) > int(policy.MaxFilenameBytes) {
			return archiveSecurityError("archive_path_unsafe", "archive entry name is too long")
		}
		cleanName, err := safeArchivePath(entry.Name)
		if err != nil {
			return archiveSecurityError("archive_path_unsafe", err.Error())
		}
		if _, ok := seen[cleanName]; ok {
			return archiveSecurityError("archive_path_unsafe", "archive has duplicate entry path")
		}
		seen[cleanName] = struct{}{}
		if entry.FileInfo().IsDir() {
			continue
		}
		if archiveEntryIsLink(entry.FileInfo()) {
			return archiveSecurityError("archive_link_rejected", "archive links are not allowed")
		}
		size := int64(entry.UncompressedSize)
		if size > policy.MaxSingleEntryBytes {
			return archiveSecurityError("archive_too_large", "archive entry exceeds max single entry bytes")
		}
		total += size
		if total > policy.MaxTotalUncompressedBytes {
			return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
		}
		if compressedSize > 0 && float64(total)/float64(compressedSize) > policy.MaxCompressionRatio {
			return archiveSecurityError("archive_ratio_exceeded", "archive compression ratio exceeds limit")
		}
		rc, err := entry.Open()
		if err != nil {
			return sevenZipOpenError(err, policy)
		}
		counted, countErr := countSingleArchiveEntry(ctx, rc, compressedSize, policy)
		closeErr := rc.Close()
		if countErr != nil {
			return countErr
		}
		if closeErr != nil {
			return sevenZipOpenError(closeErr, policy)
		}
		if counted > size && size > 0 {
			return archiveSecurityError("archive_too_large", "archive entry exceeded declared size")
		}
	}
	return nil
}

func inspectTarArchive(ctx context.Context, reader io.Reader, policy config.ArchiveGuardPolicy) error {
	tr := tar.NewReader(reader)
	total := int64(0)
	entries := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return archiveSecurityError("archive_unsupported_method", "tar archive could not be parsed")
		}
		entries++
		if entries > policy.MaxEntries {
			return archiveSecurityError("archive_too_many_entries", "archive has too many entries")
		}
		if len(header.Name) > int(policy.MaxFilenameBytes) {
			return archiveSecurityError("archive_path_unsafe", "archive entry name is too long")
		}
		if _, err := safeArchivePath(header.Name); err != nil {
			return archiveSecurityError("archive_path_unsafe", err.Error())
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return archiveSecurityError("archive_link_rejected", "archive links are not allowed")
		}
		if header.Size > policy.MaxSingleEntryBytes {
			return archiveSecurityError("archive_too_large", "archive entry exceeds max single entry bytes")
		}
		total += header.Size
		if total > policy.MaxTotalUncompressedBytes {
			return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
		}
	}
}

func archiveEntryIsLink(info fs.FileInfo) bool {
	if info == nil {
		return false
	}
	return info.Mode()&fs.ModeSymlink != 0
}

func countArchiveStream(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	_, err := countSingleArchiveEntry(ctx, reader, compressedSize, policy)
	return err
}

func countSingleArchiveEntry(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) (int64, error) {
	buf := make([]byte, policy.DecompressBufferBytes)
	total := int64(0)
	probed := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return total, archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
		}
		n, err := reader.Read(buf)
		if n > 0 {
			total += int64(n)
			probed += int64(n)
			if total > policy.MaxSingleEntryBytes || total > policy.MaxTotalUncompressedBytes {
				return total, archiveSecurityError("archive_too_large", "archive expanded size exceeds limit")
			}
			if compressedSize > 0 && float64(total)/float64(compressedSize) > policy.MaxCompressionRatio {
				return total, archiveSecurityError("archive_ratio_exceeded", "archive compression ratio exceeds limit")
			}
			if probed > policy.MaxProbeBytes {
				return total, archiveSecurityError("archive_inspection_timeout", "archive inspection probe byte limit exceeded")
			}
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, archiveSecurityError("archive_unsupported_method", "archive stream could not be decompressed")
		}
	}
}

func sevenZipOpenError(err error, policy config.ArchiveGuardPolicy) error {
	var readErr *sevenzip.ReadError
	if errors.As(err, &readErr) && readErr.Encrypted && !policy.AllowEncrypted {
		return archiveSecurityError("archive_unsupported_method", "encrypted archive entries are not allowed")
	}
	return archiveSecurityError("archive_unsupported_method", "7z archive entry could not be inspected")
}

func safeArchivePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive entry path is empty")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || strings.Contains(name, `\`) {
		return "", fmt.Errorf("archive entry path is unsafe")
	}
	cleanName := path.Clean(name)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") {
		return "", fmt.Errorf("archive entry path is unsafe")
	}
	return cleanName, nil
}

func archiveKindFor(contentType, originalName string) archiveKind {
	contentType = normalizeContentType(contentType)
	switch contentType {
	case "application/zip", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return archiveZip
	case "application/gzip", "application/x-gzip":
		return archiveGzip
	case "application/x-tar":
		return archiveTar
	case "application/zstd", "application/x-zstd":
		return archiveZstd
	case "application/x-brotli", "application/br":
		return archiveBrotli
	case "application/x-bzip2":
		return archiveBzip2
	case "application/x-xz":
		return archiveXZ
	case "application/x-7z-compressed":
		return archive7z
	}
	lowerName := strings.ToLower(originalName)
	switch {
	case strings.HasSuffix(lowerName, ".zip"), strings.HasSuffix(lowerName, ".docx"), strings.HasSuffix(lowerName, ".xlsx"), strings.HasSuffix(lowerName, ".pptx"):
		return archiveZip
	case strings.HasSuffix(lowerName, ".gz"), strings.HasSuffix(lowerName, ".tgz"):
		return archiveGzip
	case strings.HasSuffix(lowerName, ".tar"):
		return archiveTar
	case strings.HasSuffix(lowerName, ".zst"), strings.HasSuffix(lowerName, ".zstd"):
		return archiveZstd
	case strings.HasSuffix(lowerName, ".br"), strings.HasSuffix(lowerName, ".brotli"):
		return archiveBrotli
	case strings.HasSuffix(lowerName, ".bz2"), strings.HasSuffix(lowerName, ".bzip2"):
		return archiveBzip2
	case strings.HasSuffix(lowerName, ".xz"):
		return archiveXZ
	case strings.HasSuffix(lowerName, ".7z"):
		return archive7z
	default:
		return archiveUnknown
	}
}

func archiveKindFromMagic(prefix []byte) archiveKind {
	switch {
	case len(prefix) >= 4 && string(prefix[:4]) == "PK\x03\x04":
		return archiveZip
	case len(prefix) >= 2 && prefix[0] == 0x1f && prefix[1] == 0x8b:
		return archiveGzip
	case len(prefix) >= 4 && prefix[0] == 0x28 && prefix[1] == 0xb5 && prefix[2] == 0x2f && prefix[3] == 0xfd:
		return archiveZstd
	case len(prefix) >= 3 && string(prefix[:3]) == "BZh":
		return archiveBzip2
	case len(prefix) >= 6 && prefix[0] == 0xfd && string(prefix[1:6]) == "7zXZ\x00":
		return archiveXZ
	case len(prefix) >= 6 && prefix[0] == 0x37 && prefix[1] == 0x7a && prefix[2] == 0xbc && prefix[3] == 0xaf && prefix[4] == 0x27 && prefix[5] == 0x1c:
		return archive7z
	default:
		return archiveUnknown
	}
}

func archiveSecurityError(code, message string) error {
	return securityUploadError{
		status:  http.StatusUnsupportedMediaType,
		code:    code,
		message: message,
	}
}
