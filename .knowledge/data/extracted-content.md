---
id: data:extracted-content
type: data
title: Extracted Content
---
Extracted content stores search-oriented text, metadata, and OCR output derived from an uploaded file.

```yaml
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
      - text/plain; charset=utf-8
      - application/json
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
  - flow:text-extraction-generation
  - flow:ocr-extraction-generation
  - policy:search-extraction-policy
```
