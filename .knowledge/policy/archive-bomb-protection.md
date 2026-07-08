---
id: policy:archive-bomb-protection
type: policy
title: Archive Bomb Protection
---
Archive bomb protection rejects compressed inputs that can exhaust memory, disk, CPU, or worker time through expansion, nesting, entry fanout, or path tricks.

```yaml
rules:
  scope:
    - zip
    - tar
    - gzip
    - zstd
    - brotli
    - bzip2
    - xz
    - 7z when supported
    - Office Open XML and OpenDocument containers when inspection is enabled
  posture:
    default: deny archives when archive_guard.enabled true and inspection cannot complete
    metadata_trust: claimed sizes may reject early but cannot prove allow or drive allocation alone
    upload_path: never fully decompress archive in request process memory
    non_archive_storage_path: stream directly to final system:s3-storage key
    archive_storage_path: stream original compressed bytes to sibling .tmp key, inspect from system:s3-storage, copy to final key only after guard allow
    failed_archive_storage_path: delete .tmp key on guard, copy, or upload failure
  format_size_metadata:
    zip:
      location: local header, optional data descriptor, central directory, ZIP64 extra fields
      granularity: per entry compressed and uncompressed sizes
      caveat: local header may contain zero or placeholder values when data descriptor is used
    gzip:
      location: trailer ISIZE, not header
      granularity: single stream original size modulo 4294967296
      caveat: absent for early decision and wraps for content larger than 4 GiB
    zstd:
      location: frame header content size flag
      granularity: frame content size when present
      caveat: content size is optional and skippable or streaming frames may omit it
    brotli:
      location: no reliable global uncompressed size header
      granularity: stream-count only
      caveat: must decompress through bounded counter
    tar:
      location: per-entry header
      granularity: per entry stored size
      caveat: tar itself is not compressed but may be wrapped by gzip, zstd, bzip2, or xz
  inspection_order:
    - detect archive from bounded prefix via system:content-detector
    - parse archive directory or headers with bounded reader when format supports it
    - reject immediately when declared uncompressed size, entry count, depth, filename length, or path safety exceeds limit
    - after declared archive entry size and aggregate limits pass, read only data:security-policy-config mime_magic.prefix_bytes from zip, tar, and 7z entries and verify detected MIME is compatible with known entry extensions
    - recursively inspect nested archives found inside zip, tar, and 7z entries until max_depth, including entries identified by magic bytes without an archive extension
    - count entry fanout and declared or counted expanded size in one shared recursive budget across sibling nested archives and nested compressed streams
    - when a compressed stream filename indicates a tar wrapper such as tgz, tar.gz, tar.zst, tar.bz2, or tar.xz, decompress into tar reader and enforce tar entry checks
    - for formats without reliable directory metadata, stream-count decompression in bounded worker
    - stop reading as soon as any byte, ratio, entry, depth, or time limit is crossed
    - return data:security-check-result with archive_bomb_detected reason on reject
  limits:
    max_total_uncompressed_bytes: data:security-policy-config archive_guard.max_total_uncompressed_bytes
    max_compression_ratio: data:security-policy-config archive_guard.max_compression_ratio
    max_entries: data:security-policy-config archive_guard.max_entries
    max_depth: data:security-policy-config archive_guard.max_depth
    max_single_entry_bytes: data:security-policy-config archive_guard.max_single_entry_bytes
    max_filename_bytes: data:security-policy-config archive_guard.max_filename_bytes
    max_inspection_time_ms: data:security-policy-config archive_guard.max_inspection_time_ms
    max_probe_bytes: data:security-policy-config archive_guard.max_probe_bytes
    worker_memory_bytes: data:security-policy-config archive_guard.worker_memory_bytes
  reject:
    - encrypted archives unless explicitly allowed
    - symlink entries in archives
    - hardlink entries in tar archives
    - device, FIFO, socket, or other special filesystem entries in archives
    - zip, tar, or 7z entries whose known extension conflicts with bounded-prefix detected MIME
    - zip, tar, or 7z script entries unless explicitly allowed by data:security-policy-config mime_magic.allowed_script_types or allowed_script_extensions
    - recursive nested archives beyond max_depth
    - nested archives or compressed streams whose own or aggregate recursive entry count, declared uncompressed size, counted expanded size, path safety, link policy, or type checks fail
    - compressed tar wrappers whose tar entries violate count, size, path, link, or type policy
    - zip slip paths with absolute path, Windows drive-letter path, backslash path, or parent traversal
    - duplicate confusing paths when policy disallows them
    - archive central directory claims that exceed limits
    - decompression requiring unsupported methods
    - total counted uncompressed bytes greater than max_total_uncompressed_bytes
    - compression ratio greater than max_compression_ratio
    - single entry greater than max_single_entry_bytes
    - inspection exceeds max_probe_bytes or max_inspection_time_ms before allow decision
    - parser inconsistency between local headers and central directory
  behavior:
    - inspect metadata without extracting to shared filesystem
    - do not spool full uploaded archive bytes to process memory or container-local temp storage
    - use S3 Range reads as io.ReaderAt for formats requiring random access such as zip and 7z
    - inspect zip entry content type from bounded prefix only after central-directory size checks pass
    - inspect tar entry content type from bounded prefix only after tar header size checks pass
    - inspect 7z entry content type from bounded prefix only after 7z header size checks pass
    - apply policy:file-intake-security script rejection to zip, tar, and 7z entry names and shebang prefixes
    - allow generic text detection for known text-derived entry extensions when policy:file-intake-security would treat the pair as browser-generic compatible
    - inspect nested zip and 7z entries from bounded in-memory bytes only after the parent entry declared size is within max_single_entry_bytes
    - inspect nested archive magic bytes from zip, tar, and 7z entry prefixes even when the entry filename has no archive extension
    - inspect tar wrappers inside gzip, zstd, brotli, bzip2, and xz streams by streaming decompression into the tar inspector
    - only regular files and directories are accepted as archive entries; links and special filesystem nodes are rejected
    - inspect nested streaming archives with counted decompression under the same archive_guard limits
    - add counted uncompressed bytes from nested gzip, zstd, brotli, bzip2, and xz streams to the shared recursive archive budget
    - reject when sibling nested archives or compressed streams collectively exceed max_entries or max_total_uncompressed_bytes even if each nested item is individually below the limit
    - stream-count decompression only inside bounded worker when metadata is insufficient
    - count bytes through io.Copy style discard sink with limit reader, never bytes.Buffer accumulation
    - use small fixed scratch buffers capped by archive_guard.decompress_buffer_bytes
    - never allocate output buffer from claimed uncompressed size
    - apply per-entry and aggregate counters before allocating derived output buffers
    - fail closed on parse ambiguity in strict mode
  implementation_libraries:
    zstd: github.com/klauspost/compress/zstd
    brotli: github.com/andybalholm/brotli
    xz: github.com/xi2/xz with worker_memory_bytes as dictionary cap
    bzip2: Go standard library compress/bzip2
    seven_zip: github.com/bodgit/sevenzip
  result:
    reject_http_status: 415
    reject_error_code: archive_policy_violation
    reason_codes:
      - archive_bomb_detected
      - archive_too_large
      - archive_ratio_exceeded
      - archive_too_many_entries
      - archive_too_deep
      - archive_path_unsafe
      - archive_link_rejected
      - archive_special_file_rejected
      - archive_entry_type_mismatch
      - archive_entry_script_rejected
      - archive_inspection_timeout
      - archive_unsupported_method
references:
  - policy:file-intake-security
  - data:security-check-result
  - data:security-policy-config
  - system:content-detector
  - system:s3-storage
```
