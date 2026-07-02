---
id: policy:file-type-sanitization-policy
type: policy
title: File Type Sanitization Policy
---
File type sanitization policy applies configurable per-family inspection and sanitize behavior before durable upload acceptance.

```yaml
rules:
  default_posture: secure_by_default
  config_source: data:security-policy-config file_sanitization
  upload_path:
    - run resource limits from policy:resource-limit-policy before deeper parsing
    - run structural validation from policy:structural-validation-policy when parser exists
    - run prefix or chunk-only checks before storage upload when bounded reads are enough
    - run sanitizer before upload processing when original bytes must be transformed
    - do not use io.MultiWriter for sanitize transforms
    - sanitized bytes become the bytes streamed to system:s3-storage
    - in-memory sanitized buffer is allowed only within configured memory limit
    - for full-scan formats, store candidate bytes to sibling .tmp key or bounded work file and commit final key only after allow
  images_and_videos:
    scope:
      - EXIF-capable image formats
      - EXIF-capable video formats
    default: sanitize_metadata
    preserve_metadata_whitelist:
      - Orientation
      - ICC Profile
    remove_metadata:
      - GPS location
      - capture timestamp
      - camera or device model
      - serial numbers
      - author
      - comments
      - identifying EXIF, XMP, IPTC, QuickTime, or container tags
    constraints:
      - do not re-encode media
      - preserve media payload bytes when possible
      - reject if sanitizer would require lossy rewrite or unsupported container rewrite
      - apply max file size and image dimension limits
    optional_modes:
      accept_as_is: no metadata inspection or sanitize
      reject_on_sensitive_metadata: reject when privacy-sensitive metadata exists
  office_and_pdf:
    scope:
      - docx
      - xlsx
      - pptx
      - pdf
    default: reject_active_content
    behavior:
      - full file scan required
      - full scan may run in parallel worker after request body is staged
      - reject unsupported active or executable content
      - optional sanitize only when implementation can remove feature without corrupting file
    references:
      - policy:document-active-content-policy
  legacy_office:
    scope:
      - doc
      - xls
      - ppt
    default: reject
    reason: legacy binary formats have larger attack surface and unreliable inspection
    optional_modes:
      accept_as_is: store without inspection or sanitization
  svg:
    default: reject_active_or_external_content
    behavior:
      - use SAX or streaming XML parser when implementation supports it
      - otherwise treat as full-scan format and inspect before final commit
    references:
      - policy:svg-security-policy
  reject:
    - configured maximum file size exceeded
    - resource parser limit exceeded
    - structural validation failed
    - active content found in default reject-active policy
    - sanitizer unavailable for required sanitize mode
    - sanitized output exceeds configured limits
  result:
    reject_http_status: 415
    reject_error_code: file_sanitization_policy_violation
references:
  - data:security-policy-config
  - policy:file-intake-security
  - flow:security-gated-upload-acceptance
  - policy:metadata-stripping-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - policy:document-active-content-policy
  - policy:svg-security-policy
  - system:s3-storage
```
