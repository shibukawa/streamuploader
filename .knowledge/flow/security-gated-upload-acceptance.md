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
        - inspect bounded prefix
        - stream accepted bytes to system:s3-storage
    - name: run_security_gate
      mode: sequential
      actions:
        - verify policy:file-intake-security result
        - run system:clamav scan when enabled
        - reject or quarantine failed files
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
  - system:clamav
  - flow:image-thumbnail-generation
  - flow:document-preview-generation
  - flow:svg-preview-generation
  - flow:video-preview-generation
  - flow:download-variant-generation
  - requirement:application-metadata-submit
```
