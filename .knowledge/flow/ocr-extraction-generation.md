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
    - normalize and chunk recognized text
    - store ocr_text artifact
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
