---
id: policy:resource-limit-policy
type: policy
title: Resource Limit Policy
---
Resource limit policy rejects uploads that exceed configured parser and content limits before expensive inspection, sanitization, or derived processing.

```yaml
rules:
  config_source: data:security-policy-config resource_limits
  default: enabled
  limits:
    max_file_size_bytes: applies to all supported file types
    max_decompressed_size_bytes: applies to archives, office containers, PDF streams, and compressed media metadata when measurable
    max_pdf_page_count: applies to PDF inspection and preview
    max_image_width: applies to decoded image headers and SVG intrinsic width or viewBox width
    max_image_height: applies to decoded image headers and SVG intrinsic height or viewBox height
    max_image_pixel_count: width multiplied by height, including SVG intrinsic dimensions or viewBox dimensions when available
    max_object_count: applies to PDF objects, XML nodes, archive entries, and format-specific records
    max_xml_depth: applies to SVG and Office XML parts
    max_zip_entries: applies to ZIP, OOXML, and other ZIP-derived containers
    max_embedded_object_count: applies to Office and PDF
    max_parser_time_ms: per format bounded parser budget
    max_sanitized_memory_bytes: maximum in-memory sanitized bytes buffer
    max_upload_keys_per_owner: active key_created or uploading keys allowed per owner cookie
  enforcement_order:
    - reject create_upload_key when active owner key count reaches max_upload_keys_per_owner
    - reject declared Content-Length or create_upload_key size_bytes above max_file_size_bytes when available
    - count request body bytes while reading
    - parse only bounded prefix when enough for header limits
    - reject on header-declared dimensions, SVG root dimensions, SVG viewBox dimensions, counts, or decompressed sizes exceeding limits
    - run bounded full scan when format requires full inspection
    - stop parser immediately when any counter or time budget exceeds limit
  allocation_rules:
    - never allocate buffer from untrusted declared size without cap
    - use fixed scratch buffers for streaming parse
    - keep in-memory sanitize output only below max_sanitized_memory_bytes
    - stage larger sanitize or full-scan candidates outside request memory
  result:
    reject_http_status: 413 or 415
    reject_error_code: resource_limit_exceeded
    reason_codes:
      - file_too_large
      - decompressed_size_too_large
      - pdf_too_many_pages
      - image_dimensions_too_large
      - image_pixel_count_too_large
      - object_count_exceeded
      - xml_depth_exceeded
      - zip_entry_count_exceeded
      - embedded_object_count_exceeded
      - parser_timeout
      - too_many_upload_keys
references:
  - data:security-policy-config
  - policy:file-intake-security
  - policy:file-type-sanitization-policy
  - policy:archive-bomb-protection
  - data:security-check-result
```
