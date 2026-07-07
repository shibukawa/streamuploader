---
id: system:document-converter
type: system
title: Document Converter
---
Document converter is an isolated worker using embedded thumbnail extraction, headless LibreOffice when needed, and a PDF renderer to create previews.

```yaml
components:
  embedded_thumbnail_extractor:
    purpose:
      - read Office package thumbnail before LibreOffice conversion
      - read legacy Office summary thumbnail when a configured tool supports it
    behavior:
      - validate extracted image under thumbnail limits
      - re-encode through policy:preview-format-policy
      - prefer AVIF output and WebP fallback per policy:preview-format-policy
      - fall back to LibreOffice when absent, too small, corrupt, or unsafe
  office_to_pdf:
    tool: LibreOffice headless
    command_style: convert to PDF in temporary directory
  pdf_renderer:
    preferred: MuPDF mutool draw
    candidates:
      - MuPDF
      - Poppler fallback when explicitly installed
      - ImageMagick with strict policy
      - qlmanage on macOS as optional thumbnail fallback
      - sips on macOS as optional first-page thumbnail fallback when supported
    primary_use:
      - render validated PDFs and LibreOffice-normalized documents to preview images
      - keep PDF preview in document pipeline instead of generic image thumbnail pipeline
    fallback_use:
      - allow sips or qlmanage after PDF validation for low-friction local development
      - report backend as macOS fallback so production deployments can prefer deterministic renderers
constraints:
  - run outside request path when possible
  - isolate process and temporary files
  - disable network access
  - limit CPU, memory, wall time, page count, pixel count, and output size
  - remove macros and active content from generated artifacts
  - never pass unvalidated PDF to sips, qlmanage, or generic image conversion
  - treat conversion failure as preview failure, not original file corruption
references:
  - flow:document-preview-generation
  - data:thumbnail-generation-config
  - requirement:expanded-thumbnail-source-support
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
