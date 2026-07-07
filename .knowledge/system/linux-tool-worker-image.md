---
id: system:linux-tool-worker-image
type: system
title: Linux Tool Worker Image
---
Linux tool worker image provides a deterministic external-tool runtime for preview, thumbnail, document, OCR, metadata, and variant jobs.

```yaml
purpose:
  - ship common Linux binaries required by worker features
  - reduce host dependency drift
  - make startup capability probes reproducible
  - provide fallback tools for text and metadata cases not covered by Go libraries
  - provide preferred fast native tools for image and video transforms
image_roles:
  combined_service:
    description: Go service and worker run in same image for small deployments
  worker_only:
    description: API service stays slim; background worker uses tool image
  tools_nooffice:
    description: tool worker image excluding LibreOffice for faster pull/start when Office conversion is not required
packages:
  required_base:
    - ca-certificates
    - file
    - tini
    - fonts for expected document languages
  media:
    - ffmpeg
  office_preview:
    - libreoffice
    - poppler-utils when MuPDF package is unavailable in base distro
  pdf_preview:
    - MuPDF when package exists in base distro
    - poppler-utils acceptable in tool Docker when MuPDF package is unavailable
  text_extraction:
    - mutool for PDF text or metadata fallback when supported
    - poppler-utils for pdftotext when tool Docker uses Poppler fallback
    - libimage-exiftool-perl
    - Apache Tika optional
    - pandoc optional
    - catdoc or antiword optional for legacy .doc
  svg_preview:
    - librsvg2-bin for rsvg-convert
    - inkscape optional high fidelity fallback
  ocr:
    - tesseract-ocr
    - language packs selected by deployment
    - OCRmyPDF optional for searchable PDF generation
  sidecars:
    - Apache Tika Server optional
    - Gotenberg optional
    - Unstructured optional
  download_variant:
    - zstd
  metadata_strip:
    - libimage-exiftool-perl optional
variants:
  default:
    includes:
      - LibreOffice
      - Poppler when MuPDF package is unavailable
    excludes:
      - MuPDF package when unavailable in selected base distro
  nooffice:
    includes:
      - Poppler when MuPDF package is unavailable
      - media, SVG, OCR, metadata, and download variant tools
    excludes:
      - LibreOffice
      - soffice and libreoffice symlinks
      - MuPDF package when unavailable in selected base distro
pdf_tool_install_order:
  - remove lower-priority PDF renderer packages before installing MuPDF when the base distro provides a MuPDF package
  - keep Poppler in tool Docker when MuPDF package is unavailable
  - probe mutool before any fallback renderer at startup
clamav:
  - do not install ClamAV in this tool worker image
  - run ClamAV as a separate clamd container or managed scanner endpoint
  - connect from API service through system:clamav when enabled
operations:
  - run non-root
  - pin distro and package versions where reproducibility matters
  - publish detected versions in health/status
  - keep macOS-only backends outside Linux image
references:
  - system:external-tool-registry
  - system:text-extractor
  - system:ocr-engine
  - policy:tool-backend-selection-policy
```
