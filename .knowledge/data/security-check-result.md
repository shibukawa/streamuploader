---
id: data:security-check-result
type: data
title: Security Check Result
---
Security check result records lightweight file inspection outcomes before optional malware scanning.

```yaml
fields:
  declared_content_type: string optional
  detected_content_type: string
  normalized_declared_content_type: string optional
  normalized_detected_content_type: string
  detected_extension: string optional
  detector:
    enum:
      - go_http_detect_content_type
      - gabriel_vasile_mimetype
      - h2non_filetype
      - libmagic
      - magika optional
      - go_enry optional
  confidence: number optional
  mismatch:
    enum:
      - none
      - declared_vs_detected
      - extension_vs_detected
      - polyglot_suspected
  script_detection:
    shebang: string optional
    language: string optional
    executable_text: boolean
  archive_detection:
    is_archive: boolean
    nested: boolean optional
    estimated_uncompressed_bytes: integer optional
    counted_uncompressed_bytes: integer optional
    compression_ratio: number optional
    entry_count: integer optional
    max_depth: integer optional
    encrypted: boolean optional
    unsafe_path: boolean optional
    inspection_complete: boolean optional
    inspection_time_ms: integer optional
  sanitization:
    mode: string optional
    performed: boolean
    output_size_bytes: integer optional
    metadata_removed: list optional
    preserved_metadata: list optional
  structural_validation:
    performed: boolean
    valid: boolean optional
    validator: string optional
  resource_limits:
    checked: boolean
    exceeded: list optional
  active_content:
    inspected: boolean
    detected_features: list optional
  decision:
    enum:
      - allow
      - reject
      - require_deep_scan
      - quarantine
  reason_codes:
    type: list
    values:
      - content_type_match
      - content_type_mismatch
      - detected_type_unknown
      - prefix_read_failed
      - archive_policy_pending
      - archive_bomb_detected
      - archive_too_large
      - archive_ratio_exceeded
      - archive_too_many_entries
      - archive_too_deep
      - archive_path_unsafe
      - archive_inspection_timeout
      - archive_unsupported_method
      - file_too_large
      - resource_limit_exceeded
      - structural_validation_failed
      - sensitive_metadata_detected
      - sanitizer_unavailable
      - document_active_content_rejected
      - svg_active_content_rejected
      - markup_active_content_rejected
      - markup_script_detected
      - markup_iframe_detected
      - markup_external_reference_detected
      - xml_external_entity_detected
      - xml_entity_expansion_limit_exceeded
  error:
    http_status: integer optional
    code: string optional
    message: string optional
references:
  - policy:file-intake-security
  - requirement:mime-magic-consistency
  - decision:mime-detector-library
  - policy:archive-bomb-protection
  - policy:file-type-sanitization-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - policy:document-active-content-policy
  - policy:svg-security-policy
  - policy:markup-active-content-policy
```
