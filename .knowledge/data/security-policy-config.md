---
id: data:security-policy-config
type: data
title: Security Policy Config
---
Security policy config is a YAML file loaded at streamuploader startup for file intake filtering.

```yaml
source:
  env_path:
    primary: SU_SECURITY_CONFIG
    legacy_compat: SECURITY_CONFIG
  read_time: process startup before listening
  missing_path: use built-in defaults
  invalid_yaml: fail startup
  validation:
    method: built-in JSON Schema before unmarshalling policy
    unknown_keys: fail startup
    wrong_types: fail startup
  env_only_security:
    clamav:
      enabled_by: SU_CLAMAV_HOST or CLAMAV_HOST set to host:port
      disabled_by_default: true
      reason: scanner location is deployment environment specific
      optional_env:
        - SU_CLAMAV_ENABLED
        - SU_CLAMAV_SCAN_TIMEOUT_MS
        - SU_CLAMAV_STREAM_CHUNK_BYTES
schema:
  mime_magic:
    enabled:
      type: bool
      default: true
      meaning: opt-out switch for declared MIME versus magic-header check
    prefix_bytes:
      type: integer
      default: 3072
      constraints:
        min: 512
        max: 1048576
    reject_script_uploads:
      type: bool
      default: true
    allowed_script_types:
      type: map string bool
      default: {}
      meaning: opt-in accepted script families detected from shebang
      example:
        shell: true
        python: true
    allowed_script_extensions:
      type: map string bool
      default: {}
      meaning: opt-in accepted script filename extensions when extension policy is desired
      example:
        sh: true
        py: true
    allow_mime_types:
      type: map string bool
      default: {}
      meaning: if non-empty, detected or declared MIME must be in list or equivalent group
      example:
        application/pdf: true
    allow_file_types:
      type: map string bool
      default: {}
      meaning: category or short file type aliases expanded to allow_mime_types
      example:
        images: true
        pdf: true
      values:
        categories:
          - images
          - documents
          - archives
          - audio
          - videos
          - text
        examples:
          - png
          - jpeg
          - pdf
          - csv
          - zip
    deny_mime_types:
      type: map string bool
      default: {}
      meaning: detected or declared MIME in list is rejected before allow checks
      example:
        application/x-executable: true
    deny_file_types:
      type: map string bool
      default: {}
      meaning: category or short file type aliases expanded to deny_mime_types
      example:
        exe: true
    equivalent_mime_types:
      type: list of list string
      default:
        - [application/xml, text/xml]
        - [image/jpeg, image/pjpeg]
        - [application/gzip, application/x-gzip]
  archive_guard:
    enabled:
      type: bool
      default: true
      meaning: enable policy:archive-bomb-protection for detected archive/container uploads
    strict:
      type: bool
      default: true
      meaning: reject when archive inspection is incomplete, ambiguous, encrypted, or unsupported
    allow_encrypted:
      type: bool
      default: false
    max_total_uncompressed_bytes:
      type: integer
      default: 536870912
      meaning: aggregate expanded bytes across all entries, default 512 MiB
    max_single_entry_bytes:
      type: integer
      default: 268435456
      meaning: expanded bytes for one entry, default 256 MiB
    max_compression_ratio:
      type: number
      default: 100
      meaning: reject when estimated or counted uncompressed bytes divided by compressed bytes exceeds limit
    max_entries:
      type: integer
      default: 10000
    max_depth:
      type: integer
      default: 3
    max_filename_bytes:
      type: integer
      default: 512
    max_inspection_time_ms:
      type: integer
      default: 5000
    max_probe_bytes:
      type: integer
      default: 67108864
      meaning: maximum compressed bytes a worker may read while trying to prove allow, default 64 MiB
    worker_memory_bytes:
      type: integer
      default: 67108864
      meaning: memory budget for archive inspection worker, default 64 MiB
    decompress_buffer_bytes:
      type: integer
      default: 32768
      constraints:
        min: 4096
        max: 1048576
      meaning: fixed scratch buffer for streaming decompression, not derived from claimed uncompressed size
  resource_limits:
    enabled:
      type: bool
      default: true
      meaning: enable policy:resource-limit-policy before deeper inspection or sanitization
    max_file_size_bytes:
      type: integer
      default: 1073741824
      meaning: global upload file size cap, default 1 GiB
    max_decompressed_size_bytes:
      type: integer
      default: 536870912
    max_pdf_page_count:
      type: integer
      default: 500
    max_image_width:
      type: integer
      default: 32768
    max_image_height:
      type: integer
      default: 32768
    max_image_pixel_count:
      type: integer
      default: 268435456
    max_object_count:
      type: integer
      default: 100000
    max_xml_depth:
      type: integer
      default: 64
    max_zip_entries:
      type: integer
      default: 10000
    max_embedded_object_count:
      type: integer
      default: 0
    max_parser_time_ms:
      type: integer
      default: 5000
    max_sanitized_memory_bytes:
      type: integer
      default: 67108864
  structural_validation:
    enabled:
      type: bool
      default: true
      meaning: enable policy:structural-validation-policy when validator exists for detected file type
    strict:
      type: bool
      default: true
      meaning: reject malformed, ambiguous, or unsupported validation results for types requiring validation
  file_sanitization:
    enabled:
      type: bool
      default: true
      meaning: enable policy:file-type-sanitization-policy
    default_mode:
      type: enum
      default: secure_default
      values:
        - secure_default
        - accept_as_is
    per_file_type:
      type: map string object
      default: {}
      meaning: override sanitize or inspection mode by file family or MIME type
      examples:
        image/jpeg:
          mode: sanitize_metadata
        video/quicktime:
          mode: sanitize_metadata
        application/pdf:
          mode: reject_active_content
        image/svg+xml:
          mode: reject_active_or_external_content
        application/msword:
          mode: reject
      mode_values:
        - sanitize_metadata
        - reject_on_sensitive_metadata
        - reject_active_content
        - reject_active_or_external_content
        - reject
        - sanitize_when_supported
        - accept_as_is
    image_video_metadata:
      default_mode: sanitize_metadata
      preserve:
        - Orientation
        - ICC Profile
      no_reencode: true
    office_pdf:
      default_mode: reject_active_content
      full_scan_required: true
    legacy_office:
      default_mode: reject
    svg:
      default_mode: reject_active_or_external_content
      prefer_streaming_xml_parser: true
example:
  mime_magic:
    enabled: true
    prefix_bytes: 3072
    reject_script_uploads: true
    allowed_script_types:
      shell: false
      python: false
    allowed_script_extensions:
      sh: false
      py: false
    allow_file_types:
      images: true
      pdf: true
    allow_mime_types:
      text/plain: true
    deny_file_types:
      exe: true
    deny_mime_types:
      application/x-executable: true
      application/x-sharedlib: true
      application/x-msdownload: true
    equivalent_mime_types:
      - [image/jpeg, image/pjpeg]
      - [application/gzip, application/x-gzip]
  archive_guard:
    enabled: true
    strict: true
    allow_encrypted: false
    max_total_uncompressed_bytes: 536870912
    max_single_entry_bytes: 268435456
    max_compression_ratio: 100
    max_entries: 10000
    max_depth: 3
    max_filename_bytes: 512
    max_inspection_time_ms: 5000
    max_probe_bytes: 67108864
    worker_memory_bytes: 67108864
    decompress_buffer_bytes: 32768
  resource_limits:
    enabled: true
    max_file_size_bytes: 1073741824
    max_decompressed_size_bytes: 536870912
    max_pdf_page_count: 500
    max_image_width: 32768
    max_image_height: 32768
    max_image_pixel_count: 268435456
    max_object_count: 100000
    max_xml_depth: 64
    max_zip_entries: 10000
    max_embedded_object_count: 0
    max_parser_time_ms: 5000
    max_sanitized_memory_bytes: 67108864
  structural_validation:
    enabled: true
    strict: true
  file_sanitization:
    enabled: true
    default_mode: secure_default
    per_file_type:
      image/jpeg:
        mode: sanitize_metadata
      image/png:
        mode: sanitize_metadata
      video/quicktime:
        mode: sanitize_metadata
      application/pdf:
        mode: reject_active_content
      application/vnd.openxmlformats-officedocument.wordprocessingml.document:
        mode: reject_active_content
      application/vnd.openxmlformats-officedocument.spreadsheetml.sheet:
        mode: reject_active_content
      application/vnd.openxmlformats-officedocument.presentationml.presentation:
        mode: reject_active_content
      image/svg+xml:
        mode: reject_active_or_external_content
      application/msword:
        mode: reject
      application/vnd.ms-excel:
        mode: reject
      application/vnd.ms-powerpoint:
        mode: reject
    image_video_metadata:
      default_mode: sanitize_metadata
      preserve:
        - Orientation
        - ICC Profile
      no_reencode: true
    office_pdf:
      default_mode: reject_active_content
      full_scan_required: true
    legacy_office:
      default_mode: reject
    svg:
      default_mode: reject_active_or_external_content
      prefer_streaming_xml_parser: true
references:
  - policy:file-intake-security
  - system:clamav
  - requirement:mime-magic-consistency
  - decision:mime-detector-library
  - policy:archive-bomb-protection
  - policy:file-type-sanitization-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - policy:document-active-content-policy
  - policy:svg-security-policy
```
