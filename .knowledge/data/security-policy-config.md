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
        powershell: true
        batch: true
        make: true
    allowed_script_extensions:
      type: map string bool
      default: {}
      meaning: opt-in accepted script filename extensions when extension policy is desired
      example:
        sh: true
        py: true
        ps1: true
        bat: true
        makefile: true
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
        - [application/rtf, text/rtf]
        - [application/msword, application/vnd.ms-excel, application/vnd.ms-powerpoint, application/x-ole-storage]
      meaning: MIME essence aliases used for mismatch, allow, and deny matching
    builtin_generic_mime_compatibility:
      meaning: browser generic input MIME values accepted only for declared-versus-detected mismatch suppression
      security_note: not used for allow or deny matching; does not bypass reject_script_uploads
      parser_note: parser validation is not required for MIME consistency; structured parsers run only in later processing or validation stages
      browser_context:
        basis:
          - W3C File API allows empty string when file type cannot be determined
          - MDN documents that browsers usually infer File.type from extension and client configuration, not file bytes
        browser_families:
          chromium_based: Chrome, Edge, Chromium
          gecko_based: Firefox
          webkit_based: Safari
        expected_variance:
          - common image/audio/video/document extensions usually get specific MIME
          - developer text formats may arrive as text/plain or empty despite server detection as JSON/YAML/Python-specific MIME
          - Safari may send reStructuredText as application/octet-stream while prefix detector reports text/plain
          - Safari may send Markdown as text/markdown while prefix detector reports text/html when the prefix starts with HTML-like markup
          - executables and opaque binaries may arrive as application/octet-stream or empty despite server detection as Mach-O/ELF/PE MIME
      text/plain:
        - application/json
        - application/*+json
        - application/x-python
        - application/x-ndjson
        - application/yaml
        - application/x-yaml
        - text/yaml
        - text/x-yaml
        - text/x-python
        - text/x-script.python
      detected_text_plain_accepts_declared:
        - application/json
        - application/*+json
        - application/yaml
        - application/x-yaml
        - text/yaml
        - text/x-yaml
        - text/csv
        - text/markdown
        - text/html
        - application/xhtml+xml
        - application/xml
        - text/xml
        - application/*+xml
        - text/x-python
        - application/x-python
        - text/x-script.python
        - text/javascript
        - application/javascript
        - text/x-shellscript
        - text/x-ruby
        - text/x-perl
        - application/x-httpd-php
        - text/x-powershell
        - text/x-makefile
        - application/x-bat
      detected_text_plain_excludes_declared:
        - image/*
        - audio/*
        - video/*
        - application/pdf
        - Office document MIME
        - archive MIME
        - executable MIME
        - application/octet-stream except extension fallback cases
      text/markdown:
        - text/plain
        - text/html
      application/octet-stream:
        - text/plain
        - application/vnd.microsoft.portable-executable
        - application/x-coredump
        - application/x-dosexec
        - application/x-elf
        - application/x-executable
        - application/x-mach-binary
        - application/x-msdownload
        - application/x-object
        - application/x-sharedlib
    builtin_extension_fallback:
      meaning: filename extension or well-known basename may suppress content_type_mismatch for generic text formats and classify script family
      security_note:
        - not used for allow or deny matching
        - not used to accept executable magic signatures
        - markup fallback still requires policy:markup-active-content-policy
        - script fallback still requires reject_script_uploads opt-in through allowed_script_types or allowed_script_extensions
      markdown:
        extensions: [md, markdown]
        compatible:
          text/markdown: [text/plain, text/html]
      restructuredtext:
        extensions: [rst, rest]
        compatible:
          application/octet-stream: [text/plain]
          text/plain: [text/plain]
      plain_text:
        extensions: [txt, text]
        compatible:
          application/octet-stream: [text/plain]
      script_family_extensions:
        make:
          basenames: [makefile, gnumakefile]
        batch:
          extensions: [bat, cmd]
        powershell:
          extensions: [ps1, psm1, psd1]
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
        text/markdown:
          mode: reject_active_or_external_content
        text/html:
          mode: reject_active_or_external_content
        application/xml:
          mode: reject_active_or_external_content
        application/msword:
          mode: reject
        text/rtf:
          mode: reject
        application/rtf:
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
    legacy_or_complex_documents:
      default_mode: reject
      includes:
        - legacy Office binary formats
        - RTF
    svg:
      default_mode: reject_active_or_external_content
      prefer_streaming_xml_parser: true
    markup:
      default_mode: reject_active_or_external_content
      markdown_raw_html_inspection: true
      html_active_content_inspection: true
      xml_external_entity_resolution: disabled
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
      application/vnd.microsoft.portable-executable: true
      application/x-coredump: true
      application/x-dosexec: true
      application/x-elf: true
      application/x-executable: true
      application/x-mach-binary: true
      application/x-msdownload: true
      application/x-object: true
      application/x-sharedlib: true
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
      text/markdown:
        mode: reject_active_or_external_content
      text/html:
        mode: reject_active_or_external_content
      application/xml:
        mode: reject_active_or_external_content
      application/msword:
        mode: reject
      application/vnd.ms-excel:
        mode: reject
      application/vnd.ms-powerpoint:
        mode: reject
      text/rtf:
        mode: reject
      application/rtf:
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
    legacy_or_complex_documents:
      default_mode: reject
      includes:
        - legacy Office binary formats
        - RTF
    svg:
      default_mode: reject_active_or_external_content
      prefer_streaming_xml_parser: true
    markup:
      default_mode: reject_active_or_external_content
      markdown_raw_html_inspection: true
      html_active_content_inspection: true
      xml_external_entity_resolution: disabled
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
