---
id: flow:document-preview-generation
type: flow
title: Document Preview Generation
---
Document preview generation creates preview assets for PDFs and Office documents through controlled conversion and rendering.

```yaml
flow:
  trigger: data:file-item uploaded, allowed, and selected by policy:preview-generation-policy
  inputs:
    pdf:
      - application/pdf
    office_document:
      - word processor documents
      - spreadsheets
      - presentations
      - OpenDocument formats
  steps:
    - name: prepare_worker
      actions:
        - run conversion in isolated worker
        - enforce CPU, memory, file size, page count, and timeout limits
        - use temporary working directory
    - name: normalize_to_pdf
      actions:
        - pass PDFs through validation when input is PDF
        - convert Office documents to PDF using headless LibreOffice
        - reject macros or unsafe active content by policy
    - name: render_preview
      actions:
        - render configured pages, usually first page
        - generate thumbnail image variants
        - strip document metadata from generated images
    - name: store_assets
      actions:
        - store preview images in system:s3-storage
        - optionally store normalized PDF if policy allows
        - record data:derived-asset entries
  failure:
    optional_preview:
      - keep original file accepted
      - mark preview failed or skipped
    required_preview:
      - block ready or commit until preview succeeds
      - return status with conversion or render error
references:
  - policy:preview-generation-policy
  - data:derived-asset
  - system:document-converter
  - system:s3-storage
```
