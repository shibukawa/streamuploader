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
    entry_count: integer optional
    max_depth: integer optional
  decision:
    enum:
      - allow
      - reject
      - require_deep_scan
      - quarantine
  reason_codes:
    type: list
references:
  - policy:file-intake-security
  - policy:archive-bomb-protection
```

