---
id: flow:security-gated-upload-acceptance
type: flow
title: Security Gated Upload Acceptance
---
Security gated upload acceptance runs intake checks before an upload key becomes usable by application metadata.

```yaml
flow:
  trigger: streamuploader receives file bytes for data:file-item
  steps:
    - name: inspect_upload
      actions:
        - verify upload_key exists
        - read bounded prefix from request body
        - run requirement:mime-magic-consistency before storage upload
        - enforce policy:resource-limit-policy using declared size and bounded prefix facts before expensive parsing
        - build replay reader using rule:prefix-replay
        - choose final key only when no post-stream or full-scan security gate is enabled
        - choose sibling .tmp key when system:clamav, archive guard, office/pdf full scan, SVG full scan, or sanitize staging is needed
    - name: run_pre_upload_serial_checks
      mode: sequential_before_storage_upload
      actions:
        - run chunk-only metadata and structural checks that can decide from bounded input before uploading bytes
        - run policy:structural-validation-policy before sanitizer when supported by bounded parser
        - run policy:file-type-sanitization-policy sanitizer before upload when bytes must be transformed
        - do not sanitize through io.MultiWriter
        - allow in-memory sanitized bytes only within data:security-policy-config resource_limits.max_sanitized_memory_bytes
        - stream sanitized output, not original bytes, to following upload stage
    - name: run_security_gate
      mode: mixed_parallel_and_sequential
      actions:
        - stream accepted bytes to system:s3-storage
        - when system:clamav enabled, send same bytes to clamd TCP INSTREAM through io.MultiWriter
        - cap api:session-progress-api uploaded_bytes below full completion while post-upload gate is pending, default 98 percent
        - when prefix indicates archive or container, run policy:archive-bomb-protection against .tmp object after S3 upload completes
        - when Office or PDF full scan is required, run policy:document-active-content-policy before copying .tmp object to final key
        - when SVG cannot be safely checked by streaming parser, run policy:svg-security-policy before copying .tmp object to final key
        - delete .tmp object and reject failed files
        - copy .tmp object to final key only after all enabled security gates allow
    - name: start_async_derived_work
      mode: parallel_after_security_gate
      actions:
        - enqueue preview flows selected by policy:preview-generation-policy
        - enqueue flow:download-variant-generation when selected
    - name: publish_upload_facts
      actions:
        - mark upload_key uploaded, clean, rejected, or failed
        - expose original file facts through api:upload-api
        - expose generated previews through status APIs when enabled
references:
  - policy:file-intake-security
  - policy:file-type-sanitization-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - policy:document-active-content-policy
  - policy:svg-security-policy
  - requirement:mime-magic-consistency
  - rule:prefix-replay
  - system:clamav
  - flow:image-thumbnail-generation
  - flow:document-preview-generation
  - flow:svg-preview-generation
  - flow:video-preview-generation
  - flow:download-variant-generation
  - requirement:application-metadata-submit
```
