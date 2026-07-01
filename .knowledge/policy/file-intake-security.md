---
id: policy:file-intake-security
type: policy
title: File Intake Security
---
File intake policy rejects risky uploads before durable storage whenever the decision can be made from bounded stream prefix inspection.

```yaml
rules:
  default_posture: whitelist
  prefix_inspection:
    read_limit_bytes: data:security-policy-config mime_magic.prefix_bytes default 3072
    checks:
      - system:content-detector lightweight type detection
      - magic number detection
      - known dangerous executable signatures
      - shebang and script language detection
      - archive or polyglot indicators when relevant
      - declared versus detected content type mismatch
    stream_handling:
      - read at most configured prefix bytes from http request body
      - detect type from prefix bytes only on request path
      - replay prefix before remaining body with rule:prefix-replay
      - send replayed stream to system:s3-storage only after allow decision
  declared_mime_consistency:
    enabled: default true, opt out through data:security-policy-config mime_magic.enabled false
    source_order:
      - create_upload_key content_type
      - upload request Content-Type header
    normalization:
      - parse media type and ignore parameters such as charset
      - compare canonical MIME essence values
      - allow configured equivalent groups for common aliases
    filtering:
      config_source: data:security-policy-config
      allow_mime_types: optional whitelist, empty means no whitelist
      allow_file_types: category or short-name whitelist expanded to MIME types
      deny_mime_types: explicit reject list
      deny_file_types: category or short-name deny list expanded to MIME types
      equivalent_mime_types: configured alias groups
    reject_when:
      - declared MIME exists and detected MIME conflicts
      - detected or declared MIME matches deny list
      - allow list exists and neither detected nor declared MIME is allowed
      - detected MIME is unknown in strict mode
      - content appears executable, script, or archive disallowed by later policy
    error:
      http_status: 415
      code: content_type_mismatch
      response: JSON error
  allowlist:
    managed_by: configuration
    values:
      - media type
      - extension only as secondary hint
      - magic signature
  optional_scan:
    engine: system:clamav
    mode:
      - stream scan when available
      - asynchronous scan only when product accepts quarantine workflow
  reject:
    - executable formats unless explicitly allowed
    - shell script or other script uploaded under non-script media type
    - unknown type when whitelist mode is strict
    - file exceeding configured size limit
    - archive violating policy:archive-bomb-protection
  references:
    - rule:prefix-replay
    - requirement:streaming-upload
    - requirement:mime-magic-consistency
    - data:security-policy-config
    - system:content-detector
    - decision:mime-detector-library
    - policy:archive-bomb-protection
    - data:security-check-result
```
