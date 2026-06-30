---
id: system:ocr-engine
type: system
title: OCR Engine
---
OCR engine extracts text from raster images and image-only document pages.

```yaml
backends:
  tesseract:
    command: tesseract --version
    capabilities:
      - image OCR
      - PDF page OCR after rasterization
      - language pack dependent OCR
  cloud_ocr_via_local_command:
    optional: true
    capabilities:
      - higher accuracy OCR when product policy permits external service use
    note: implemented through system:local-command-processor, not native provider webhook
  document_ai_via_local_command:
    optional: true
    capabilities:
      - layout aware OCR
      - table and form extraction
    note: command owns provider auth, request shape, and response normalization
constraints:
  - opt-in because OCR is CPU intensive and may expose private content to index systems
  - run in isolated worker
  - require configured languages
  - cap pages, pixels, wall time, CPU, memory, and output size
  - store confidence and language when backend provides them
references:
  - flow:ocr-extraction-generation
  - data:extracted-content
  - policy:search-extraction-policy
  - system:external-tool-registry
  - system:external-processing-delegates
  - system:local-command-processor
  - policy:external-delegation-policy
  - policy:processor-execution-policy
```
