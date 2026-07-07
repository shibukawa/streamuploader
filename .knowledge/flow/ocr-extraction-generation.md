---
id: flow:ocr-extraction-generation
type: flow
title: OCR Extraction Generation
---
OCR extraction generation creates searchable text from image-only documents or images when policy enables OCR.

```yaml
flow:
  trigger: policy:search-extraction-policy selects OCR for accepted file
  steps:
    - create pending task:
        actions:
          - allocate ocr_text data:derived-asset
          - use artifact object key source object key plus .text.json unless policy overrides
          - create data:async-task-marker with kind ocr_extraction for async execution
    - rasterize input:
        pdf: render selected pages with bounded DPI
        image: decode bounded image
        office: use normalized_pdf from flow:document-preview-generation when available
    - preprocess optional:
        - grayscale
        - deskew
        - resize within pixel limit
    - run OCR backend:
        preferred: tesseract
        fallback: system:local-command-processor when configured
        inputs: bounded raster pages
        outputs:
          - plain text
          - hOCR or TSV optional
          - data:processor-result when local command backend is used
    - normalize and chunk recognized text:
        output: texts.ocr in data:extracted-content JSON
    - store ocr_text artifact:
        object_key: source object key plus .text.json
        merge_behavior: preserve existing texts.title, texts.description, texts.text, and texts.extracted keys from text extraction when present
    - complete pending task:
        actions:
          - update data:derived-asset generated, skipped, or failed
          - delete data:async-task-marker after terminal success or terminal failure
limits:
  - page count
  - pixels per page
  - total pixels
  - wall time
  - CPU and memory
  - output size
references:
  - data:extracted-content
  - data:processor-result
  - system:ocr-engine
  - system:local-command-processor
  - system:document-converter
  - policy:search-extraction-policy
  - policy:processor-execution-policy
```
