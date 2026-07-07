---
id: data:extracted-content
type: data
title: Extracted Content
---
Extracted content stores search-oriented text, metadata, and OCR output derived from an uploaded file.

```yaml
artifact:
  default_object_key: source object key plus .text.json
  content_type: application/json; charset=utf-8
  shape:
    texts:
      type: object
      meaning: source-keyed text map like {"extracted":"...", "ocr":"..."}
      keys:
        text: direct body for plain text-like files when no richer source key is needed
        extracted: full extracted body for Office, PDF, HTML, JSON, CSV, XML, and other parser-backed text
        title: image or document title metadata when present and policy allows indexing it
        description: image or document description metadata when present and policy allows indexing it
        ocr: OCR text from image files, image-only PDFs, or rendered document pages
    sources:
      optional: per-key provenance, backend, page range, offsets, confidence, and warnings
    metadata:
      optional: allowlisted metadata values that are not represented as searchable text
  examples:
    plain_text:
      texts:
        text: original normalized UTF-8 text
    office_or_pdf:
      texts:
        extracted: full normalized extracted text
    image_with_metadata_and_ocr:
      texts:
        title: optional image title
        description: optional image description
        ocr: recognized text
fields:
  asset_key: derived asset key
  source_file_key: data:file-item key
  extraction_kind:
    enum:
      - extracted_text
      - extracted_metadata
      - ocr_text
  object_key: S3 object key for normalized JSON or text artifact
  content_type:
    examples:
      - application/json
      - application/json; charset=utf-8
      - text/plain; charset=utf-8 legacy or backend-local intermediate only
  language: optional BCP47 language tag
  page_count: optional integer
  character_count: optional integer
  word_count: optional integer
  chunks:
    optional: search index chunk descriptors
  status:
    enum:
      - pending
      - generated
      - skipped
      - failed
  privacy_classification:
    enum:
      - public_indexable
      - private_indexable
      - internal_only
      - do_not_index
  error_code: optional
references:
  - data:file-item
  - data:derived-asset
  - api:extracted-content-api
  - flow:text-extraction-generation
  - flow:ocr-extraction-generation
  - policy:search-extraction-policy
```
