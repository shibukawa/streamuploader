---
id: system:document-converter
type: system
title: Document Converter
---
Document converter is an isolated worker using headless LibreOffice plus a PDF renderer to create previews.

```yaml
components:
  office_to_pdf:
    tool: LibreOffice headless
    command_style: convert to PDF in temporary directory
  pdf_renderer:
    candidates:
      - Poppler
      - MuPDF
      - ImageMagick with strict policy
constraints:
  - run outside request path when possible
  - isolate process and temporary files
  - disable network access
  - limit CPU, memory, wall time, page count, pixel count, and output size
  - remove macros and active content from generated artifacts
  - treat conversion failure as preview failure, not original file corruption
references:
  - flow:document-preview-generation
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
