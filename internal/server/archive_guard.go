package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
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

func inspectArchiveObject(ctx context.Context, store storage.Store, bucket, objectKey string, compressedSize int64, kind archiveKind, originalName string, policy config.SecurityPolicy) error {
	archivePolicy := policy.ArchiveGuard
	ctx, cancel := context.WithTimeout(ctx, time.Duration(archivePolicy.MaxInspectionTimeMS)*time.Millisecond)
	defer cancel()
	switch kind {
	case archiveZip:
		budget := &archiveInspectionBudget{}
		return inspectZipArchive(ctx, s3ObjectReaderAt{ctx: ctx, store: store, bucket: bucket, key: objectKey}, compressedSize, archivePolicy, policy.MimeMagic, 1, budget)
	case archiveGzip:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, archivePolicy, func(ctx context.Context, reader io.Reader, compressedSize int64, archivePolicy config.ArchiveGuardPolicy) error {
			return inspectGzipArchiveNamed(ctx, reader, compressedSize, archivePolicy, policy.MimeMagic, originalName, 1, &archiveInspectionBudget{})
		})
	case archiveTar:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, archivePolicy, func(ctx context.Context, reader io.Reader, _ int64, archivePolicy config.ArchiveGuardPolicy) error {
			return inspectTarArchive(ctx, reader, archivePolicy, policy.MimeMagic, 1, &archiveInspectionBudget{})
		})
	case archiveZstd:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, archivePolicy, func(ctx context.Context, reader io.Reader, compressedSize int64, archivePolicy config.ArchiveGuardPolicy) error {
			return inspectZstdArchiveNamed(ctx, reader, compressedSize, archivePolicy, policy.MimeMagic, originalName, 1, &archiveInspectionBudget{})
		})
	case archiveBrotli:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, archivePolicy, func(ctx context.Context, reader io.Reader, compressedSize int64, archivePolicy config.ArchiveGuardPolicy) error {
			return inspectBrotliArchiveNamed(ctx, reader, compressedSize, archivePolicy, policy.MimeMagic, originalName, 1, &archiveInspectionBudget{})
		})
	case archiveBzip2:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, archivePolicy, func(ctx context.Context, reader io.Reader, compressedSize int64, archivePolicy config.ArchiveGuardPolicy) error {
			return inspectBzip2ArchiveNamed(ctx, reader, compressedSize, archivePolicy, policy.MimeMagic, originalName, 1, &archiveInspectionBudget{})
		})
	case archiveXZ:
		return inspectStreamArchiveObject(ctx, store, bucket, objectKey, compressedSize, archivePolicy, func(ctx context.Context, reader io.Reader, compressedSize int64, archivePolicy config.ArchiveGuardPolicy) error {
			return inspectXZArchiveNamed(ctx, reader, compressedSize, archivePolicy, policy.MimeMagic, originalName, 1, &archiveInspectionBudget{})
		})
	case archive7z:
		return inspect7zArchive(ctx, s3ObjectReaderAt{ctx: ctx, store: store, bucket: bucket, key: objectKey}, compressedSize, archivePolicy, policy.MimeMagic, 1, &archiveInspectionBudget{})
	default:
		if archivePolicy.Strict {
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

type archiveInspectionBudget struct {
	totalUncompressed int64
	entries           int64
}

func (b *archiveInspectionBudget) addEntry(size int64, policy config.ArchiveGuardPolicy) error {
	if b == nil {
		return nil
	}
	b.entries++
	if b.entries > policy.MaxEntries {
		return archiveSecurityError("archive_too_many_entries", "archive has too many entries")
	}
	b.totalUncompressed += size
	if b.totalUncompressed > policy.MaxTotalUncompressedBytes {
		return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
	}
	return nil
}

func inspectZipArchive(ctx context.Context, readerAt io.ReaderAt, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
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
			if err := budget.addEntry(0, policy); err != nil {
				return err
			}
			continue
		}
		if archiveEntryIsLink(entry.FileInfo()) {
			return archiveSecurityError("archive_link_rejected", "archive links are not allowed")
		}
		if archiveEntryIsSpecial(entry.FileInfo()) {
			return archiveSecurityError("archive_special_file_rejected", "archive special files are not allowed")
		}
		size := int64(entry.UncompressedSize64)
		if size > policy.MaxSingleEntryBytes {
			return archiveSecurityError("archive_too_large", "archive entry exceeds max single entry bytes")
		}
		if err := budget.addEntry(size, policy); err != nil {
			return err
		}
		total += size
		if total > policy.MaxTotalUncompressedBytes {
			return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
		}
		if compressedSize > 0 && float64(total)/float64(compressedSize) > policy.MaxCompressionRatio {
			return archiveSecurityError("archive_ratio_exceeded", "archive compression ratio exceeds limit")
		}
		if err := inspectZipEntryContent(ctx, entry, cleanName, policy, mimePolicy, depth, budget); err != nil {
			return err
		}
	}
	return nil
}

func inspectZipEntryContent(ctx context.Context, entry *zip.File, name string, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
	knownExtension := len(config.MIMEFileType(normalizedExtension(name))) > 0
	nestedKind := archiveKindFor("", name)
	if err := ctx.Err(); err != nil {
		return archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
	}
	rc, err := entry.Open()
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "zip archive entry could not be inspected")
	}
	defer rc.Close()
	prefix, err := readArchiveEntryPrefix(rc, mimePolicy)
	if err != nil {
		return err
	}
	if len(prefix) == 0 && knownExtension {
		return archiveSecurityError("archive_entry_type_mismatch", "archive entry content type could not be detected")
	}
	if err := inspectArchiveEntryScript(prefix, name, mimePolicy); err != nil {
		return err
	}
	detected := detectContentType(prefix)
	if knownExtension && !archiveEntryExtensionMatchesDetected(name, detected, mimePolicy) {
		ext := normalizedExtension(name)
		return archiveSecurityError("archive_entry_type_mismatch", fmt.Sprintf("archive entry %s extension .%s does not match detected content type %s", name, ext, detected))
	}
	if nestedKind == archiveUnknown {
		nestedKind = archiveKindFromMagic(prefix)
	}
	if nestedKind == archiveUnknown {
		return nil
	}
	if depth >= policy.MaxDepth {
		return archiveSecurityError("archive_too_deep", "archive nesting depth exceeds limit")
	}
	return inspectNestedArchiveEntry(ctx, entry, name, nestedKind, policy, mimePolicy, depth+1, budget)
}

func readArchiveEntryPrefix(reader io.Reader, policy config.MimeMagicPolicy) ([]byte, error) {
	limit := policy.PrefixBytes
	if limit <= 0 {
		limit = 3072
	}
	if limit < 512 {
		limit = 512
	}
	if limit > 1<<20 {
		limit = 1 << 20
	}
	prefix := make([]byte, limit)
	n, err := io.ReadFull(reader, prefix)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, archiveSecurityError("archive_unsupported_method", "archive entry prefix could not be read")
	}
	return prefix[:n], nil
}

func archiveEntryExtensionMatchesDetected(name, detected string, policy config.MimeMagicPolicy) bool {
	expected := config.MIMEFileType(normalizedExtension(name))
	for _, candidate := range expected {
		if mimeEquivalent(detected, candidate, policy.EquivalentMIMETypes) || declaredCompatibleWithDetected(candidate, detected, name) {
			return true
		}
	}
	return false
}

func inspectNestedArchiveEntry(ctx context.Context, entry *zip.File, name string, kind archiveKind, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
	rc, err := entry.Open()
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "nested archive entry could not be opened")
	}
	defer rc.Close()
	compressedSize := int64(entry.UncompressedSize64)
	return inspectNestedArchiveReader(ctx, rc, compressedSize, name, kind, policy, mimePolicy, depth, budget)
}

func inspectNestedArchiveReader(ctx context.Context, reader io.Reader, compressedSize int64, name string, kind archiveKind, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
	switch kind {
	case archiveZip:
		body, err := readArchiveEntryBytes(reader, policy.MaxSingleEntryBytes)
		if err != nil {
			return err
		}
		return inspectZipArchive(ctx, bytesReaderAt(body), int64(len(body)), policy, mimePolicy, depth, budget)
	case archiveGzip:
		return inspectGzipArchiveNamed(ctx, reader, compressedSize, policy, mimePolicy, name, depth, budget)
	case archiveTar:
		return inspectTarArchive(ctx, reader, policy, mimePolicy, depth, budget)
	case archiveZstd:
		return inspectZstdArchiveNamed(ctx, reader, compressedSize, policy, mimePolicy, name, depth, budget)
	case archiveBrotli:
		return inspectBrotliArchiveNamed(ctx, reader, compressedSize, policy, mimePolicy, name, depth, budget)
	case archiveBzip2:
		return inspectBzip2ArchiveNamed(ctx, reader, compressedSize, policy, mimePolicy, name, depth, budget)
	case archiveXZ:
		return inspectXZArchiveNamed(ctx, reader, compressedSize, policy, mimePolicy, name, depth, budget)
	case archive7z:
		body, err := readArchiveEntryBytes(reader, policy.MaxSingleEntryBytes)
		if err != nil {
			return err
		}
		return inspect7zArchive(ctx, bytesReaderAt(body), int64(len(body)), policy, mimePolicy, depth, budget)
	default:
		if policy.Strict {
			return archiveSecurityError("archive_unsupported_method", "nested archive type could not be inspected")
		}
		return nil
	}
}

func readArchiveEntryBytes(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, archiveSecurityError("archive_unsupported_method", "nested archive entry could not be read")
	}
	if int64(len(body)) > maxBytes {
		return nil, archiveSecurityError("archive_too_large", "nested archive entry exceeds size limit")
	}
	return body, nil
}

type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[int(off):])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func inspectGzipArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return inspectGzipArchiveNamed(ctx, reader, compressedSize, policy, config.MimeMagicPolicy{}, "", 1, &archiveInspectionBudget{})
}

func inspectGzipArchiveNamed(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, name string, depth int64, budget *archiveInspectionBudget) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "gzip stream could not be parsed")
	}
	defer gz.Close()
	if compressedArchiveWrapsTar(archiveGzip, name) {
		return inspectTarArchive(ctx, gz, policy, mimePolicy, depth, budget)
	}
	return countArchiveStream(ctx, gz, compressedSize, policy, budget)
}

func inspectZstdArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return inspectZstdArchiveNamed(ctx, reader, compressedSize, policy, config.MimeMagicPolicy{}, "", 1, &archiveInspectionBudget{})
}

func inspectZstdArchiveNamed(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, name string, depth int64, budget *archiveInspectionBudget) error {
	zr, err := zstd.NewReader(reader)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "zstd stream could not be parsed")
	}
	defer zr.Close()
	if compressedArchiveWrapsTar(archiveZstd, name) {
		return inspectTarArchive(ctx, zr, policy, mimePolicy, depth, budget)
	}
	return countArchiveStream(ctx, zr, compressedSize, policy, budget)
}

func inspectBrotliArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return inspectBrotliArchiveNamed(ctx, reader, compressedSize, policy, config.MimeMagicPolicy{}, "", 1, &archiveInspectionBudget{})
}

func inspectBrotliArchiveNamed(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, name string, depth int64, budget *archiveInspectionBudget) error {
	br := brotli.NewReader(reader)
	if compressedArchiveWrapsTar(archiveBrotli, name) {
		return inspectTarArchive(ctx, br, policy, mimePolicy, depth, budget)
	}
	return countArchiveStream(ctx, br, compressedSize, policy, budget)
}

func inspectBzip2Archive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return inspectBzip2ArchiveNamed(ctx, reader, compressedSize, policy, config.MimeMagicPolicy{}, "", 1, &archiveInspectionBudget{})
}

func inspectBzip2ArchiveNamed(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, name string, depth int64, budget *archiveInspectionBudget) error {
	bz := bzip2.NewReader(reader)
	if compressedArchiveWrapsTar(archiveBzip2, name) {
		return inspectTarArchive(ctx, bz, policy, mimePolicy, depth, budget)
	}
	return countArchiveStream(ctx, bz, compressedSize, policy, budget)
}

func inspectXZArchive(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy) error {
	return inspectXZArchiveNamed(ctx, reader, compressedSize, policy, config.MimeMagicPolicy{}, "", 1, &archiveInspectionBudget{})
}

func inspectXZArchiveNamed(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, name string, depth int64, budget *archiveInspectionBudget) error {
	dictMax := uint32(policy.WorkerMemoryBytes)
	if policy.WorkerMemoryBytes <= 0 || policy.WorkerMemoryBytes > int64(^uint32(0)) {
		dictMax = xz.DefaultDictMax
	}
	xr, err := xz.NewReader(reader, dictMax)
	if err != nil {
		return archiveSecurityError("archive_unsupported_method", "xz stream could not be parsed")
	}
	if compressedArchiveWrapsTar(archiveXZ, name) {
		return inspectTarArchive(ctx, xr, policy, mimePolicy, depth, budget)
	}
	return countArchiveStream(ctx, xr, compressedSize, policy, budget)
}

func compressedArchiveWrapsTar(kind archiveKind, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch kind {
	case archiveGzip:
		return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
	case archiveZstd:
		return strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tar.zstd")
	case archiveBrotli:
		return strings.HasSuffix(lower, ".tar.br") || strings.HasSuffix(lower, ".tar.brotli")
	case archiveBzip2:
		return strings.HasSuffix(lower, ".tbz") || strings.HasSuffix(lower, ".tbz2") || strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tar.bzip2")
	case archiveXZ:
		return strings.HasSuffix(lower, ".txz") || strings.HasSuffix(lower, ".tar.xz")
	default:
		return false
	}
}

func inspect7zArchive(ctx context.Context, readerAt io.ReaderAt, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
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
			if err := budget.addEntry(0, policy); err != nil {
				return err
			}
			continue
		}
		if archiveEntryIsLink(entry.FileInfo()) {
			return archiveSecurityError("archive_link_rejected", "archive links are not allowed")
		}
		if archiveEntryIsSpecial(entry.FileInfo()) {
			return archiveSecurityError("archive_special_file_rejected", "archive special files are not allowed")
		}
		size := int64(entry.UncompressedSize)
		if size > policy.MaxSingleEntryBytes {
			return archiveSecurityError("archive_too_large", "archive entry exceeds max single entry bytes")
		}
		if err := budget.addEntry(size, policy); err != nil {
			return err
		}
		total += size
		if total > policy.MaxTotalUncompressedBytes {
			return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
		}
		if compressedSize > 0 && float64(total)/float64(compressedSize) > policy.MaxCompressionRatio {
			return archiveSecurityError("archive_ratio_exceeded", "archive compression ratio exceeds limit")
		}
		counted, err := inspect7zEntryContent(ctx, entry, cleanName, compressedSize, policy, mimePolicy, depth, budget)
		if err != nil {
			return err
		}
		if counted > size && size > 0 {
			return archiveSecurityError("archive_too_large", "archive entry exceeded declared size")
		}
	}
	return nil
}

func inspect7zEntryContent(ctx context.Context, entry *sevenzip.File, name string, compressedSize int64, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
	}
	rc, err := entry.Open()
	if err != nil {
		return 0, sevenZipOpenError(err, policy)
	}
	defer rc.Close()
	prefix, err := readArchiveEntryPrefix(rc, mimePolicy)
	if err != nil {
		return 0, err
	}
	if err := inspectArchiveEntryPrefix(ctx, prefix, name, mimePolicy); err != nil {
		return 0, err
	}
	nestedKind := archiveKindFor("", name)
	if nestedKind == archiveUnknown {
		nestedKind = archiveKindFromMagic(prefix)
	}
	reader := io.MultiReader(bytes.NewReader(prefix), rc)
	if nestedKind != archiveUnknown {
		if depth >= policy.MaxDepth {
			return 0, archiveSecurityError("archive_too_deep", "archive nesting depth exceeds limit")
		}
		if err := inspectNestedArchiveReader(ctx, reader, int64(entry.UncompressedSize), name, nestedKind, policy, mimePolicy, depth+1, budget); err != nil {
			return 0, err
		}
		return int64(entry.UncompressedSize), nil
	}
	counted, err := countSingleArchiveEntry(ctx, reader, compressedSize, policy)
	if err != nil {
		return counted, err
	}
	return counted, nil
}

func inspectTarArchive(ctx context.Context, reader io.Reader, policy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
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
		if header.FileInfo().IsDir() {
			if err := budget.addEntry(0, policy); err != nil {
				return err
			}
			continue
		}
		if !tarEntryIsRegular(header) {
			return archiveSecurityError("archive_special_file_rejected", "archive special files are not allowed")
		}
		if header.Size > policy.MaxSingleEntryBytes {
			return archiveSecurityError("archive_too_large", "archive entry exceeds max single entry bytes")
		}
		if err := budget.addEntry(header.Size, policy); err != nil {
			return err
		}
		total += header.Size
		if total > policy.MaxTotalUncompressedBytes {
			return archiveSecurityError("archive_too_large", "archive exceeds max total uncompressed bytes")
		}
		if err := inspectTarEntryContent(ctx, tr, header.Name, header.Size, policy, mimePolicy, depth, budget); err != nil {
			return err
		}
	}
}

func inspectTarEntryContent(ctx context.Context, reader io.Reader, name string, compressedSize int64, archivePolicy config.ArchiveGuardPolicy, mimePolicy config.MimeMagicPolicy, depth int64, budget *archiveInspectionBudget) error {
	prefix, err := readArchiveEntryPrefix(reader, mimePolicy)
	if err != nil {
		return err
	}
	if err := inspectArchiveEntryPrefix(ctx, prefix, name, mimePolicy); err != nil {
		return err
	}
	nestedKind := archiveKindFor("", name)
	if nestedKind == archiveUnknown {
		nestedKind = archiveKindFromMagic(prefix)
	}
	if nestedKind == archiveUnknown {
		return nil
	}
	if depth >= archivePolicy.MaxDepth {
		return archiveSecurityError("archive_too_deep", "archive nesting depth exceeds limit")
	}
	return inspectNestedArchiveReader(ctx, io.MultiReader(bytes.NewReader(prefix), reader), compressedSize, name, nestedKind, archivePolicy, mimePolicy, depth+1, budget)
}

func inspectArchiveEntryPrefix(ctx context.Context, prefix []byte, name string, policy config.MimeMagicPolicy) error {
	knownExtension := len(config.MIMEFileType(normalizedExtension(name))) > 0
	if err := ctx.Err(); err != nil {
		return archiveSecurityError("archive_inspection_timeout", "archive inspection timed out")
	}
	if len(prefix) == 0 && knownExtension {
		return archiveSecurityError("archive_entry_type_mismatch", "archive entry content type could not be detected")
	}
	if err := inspectArchiveEntryScript(prefix, name, policy); err != nil {
		return err
	}
	if !knownExtension {
		return nil
	}
	detected := detectContentType(prefix)
	if archiveEntryExtensionMatchesDetected(name, detected, policy) {
		return nil
	}
	ext := normalizedExtension(name)
	return archiveSecurityError("archive_entry_type_mismatch", fmt.Sprintf("archive entry %s extension .%s does not match detected content type %s", name, ext, detected))
}

func inspectArchiveEntryScript(prefix []byte, name string, policy config.MimeMagicPolicy) error {
	script := detectScript(prefix, name)
	if !policy.RejectScriptUploads || !script.detected || scriptAllowed(script, policy) {
		return nil
	}
	return archiveSecurityError("archive_entry_script_rejected", fmt.Sprintf("archive entry %s contains a script", name))
}

func archiveEntryIsLink(info fs.FileInfo) bool {
	if info == nil {
		return false
	}
	return info.Mode()&fs.ModeSymlink != 0
}

func archiveEntryIsSpecial(info fs.FileInfo) bool {
	if info == nil {
		return false
	}
	return info.Mode()&fs.ModeType != 0
}

func tarEntryIsRegular(header *tar.Header) bool {
	if header == nil {
		return false
	}
	return header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA
}

func countArchiveStream(ctx context.Context, reader io.Reader, compressedSize int64, policy config.ArchiveGuardPolicy, budget *archiveInspectionBudget) error {
	counted, err := countSingleArchiveEntry(ctx, reader, compressedSize, policy)
	if err != nil {
		return err
	}
	return budget.addEntry(counted, policy)
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
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || strings.Contains(name, `\`) || hasWindowsDrivePrefix(name) {
		return "", fmt.Errorf("archive entry path is unsafe")
	}
	cleanName := path.Clean(name)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") {
		return "", fmt.Errorf("archive entry path is unsafe")
	}
	return cleanName, nil
}

func hasWindowsDrivePrefix(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	first := name[0]
	return (first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z')
}

func archiveKindFor(contentType, originalName string) archiveKind {
	contentType = normalizeContentType(contentType)
	switch contentType {
	case "application/zip",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.oasis.opendocument.text",
		"application/vnd.oasis.opendocument.spreadsheet",
		"application/vnd.oasis.opendocument.presentation":
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
	case strings.HasSuffix(lowerName, ".zip"),
		strings.HasSuffix(lowerName, ".docx"),
		strings.HasSuffix(lowerName, ".xlsx"),
		strings.HasSuffix(lowerName, ".pptx"),
		strings.HasSuffix(lowerName, ".odt"),
		strings.HasSuffix(lowerName, ".ods"),
		strings.HasSuffix(lowerName, ".odp"):
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
