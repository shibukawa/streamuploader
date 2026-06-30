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
    - select backend:
        by_content_type:
          text: direct UTF-8 normalization
          pdf: Go PDF text parser when quality is acceptable, fallback to pdftotext or tika
          office_document: docx or OOXML parser first, fallback to tika or libreoffice text export
          html: sanitize and extract visible text
          image: skip unless OCR policy enables flow:ocr-extraction-generation
          custom: system:local-command-processor when configured by policy:processor-execution-policy
    - extract metadata:
        backend: native Go parser first, fallback to exiftool or tika when needed
        output: extracted_metadata JSON
    - normalize text:
        - decode to UTF-8
        - normalize Unicode
        - collapse excessive whitespace
        - preserve page or section boundaries when available
        - cap maximum bytes, characters, and pages
    - create search chunks:
        - optional fixed-size or page-based chunks
        - include source offsets when available
    - store artifact:
        - extracted_text as text/plain or JSON
        - extracted_metadata as JSON
    - update file status and enqueue index handoff when configured
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
