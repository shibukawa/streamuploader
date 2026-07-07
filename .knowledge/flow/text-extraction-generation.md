---
id: flow:text-extraction-generation
type: flow
title: Text Extraction Generation
---
Text extraction generation creates normalized searchable text and metadata artifacts after file acceptance.

```yaml
flow:
  trigger: data:file-item accepted and selected by policy:search-extraction-policy
  steps:
    - create pending task:
        actions:
          - allocate extracted_text or extracted_metadata data:derived-asset
          - use artifact object key source object key plus .text.json unless policy overrides
          - create data:async-task-marker with kind text_extraction or metadata_extraction for async execution
    - select backend:
        by_content_type:
          text: direct UTF-8 normalization into texts.text
          pdf: Go PDF text parser when quality is acceptable, fallback to pdftotext or tika
          office_document: docx or OOXML parser first, fallback to tika or libreoffice text export
          html: sanitize and extract visible text
          image: skip unless OCR policy enables flow:ocr-extraction-generation
          custom: system:local-command-processor when configured by policy:processor-execution-policy
    - extract metadata:
        backend: native Go parser first, fallback to exiftool or tika when needed
        output:
          - texts.title when title-like metadata is allowlisted
          - texts.description when description-like metadata is allowlisted
          - metadata object for remaining allowlisted fields
    - normalize text:
        - decode to UTF-8
        - normalize Unicode
        - collapse excessive whitespace
        - preserve page or section boundaries when available
        - cap maximum bytes, characters, and pages
        - write parser-backed full body into texts.extracted for Office, PDF, HTML, JSON, CSV, XML, and similar formats
    - create search chunks:
        - optional fixed-size or page-based chunks
        - include source offsets when available
    - store artifact:
        - data:extracted-content JSON at source object key plus .text.json
        - include source-keyed texts map
        - include per-key sources provenance when available
    - update file status and enqueue index handoff when configured
    - complete pending task:
        actions:
          - update data:derived-asset generated, skipped, or failed
          - delete data:async-task-marker after terminal success or terminal failure
failure:
  - mark extraction failed without corrupting original file
  - include backend, timeout, size limit, or parse error code
references:
  - data:extracted-content
  - data:processor-result
  - system:text-extractor
  - system:local-command-processor
  - policy:search-extraction-policy
  - policy:processor-execution-policy
  - policy:worker-queue-policy
```
