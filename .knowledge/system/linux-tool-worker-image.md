---
id: system:linux-tool-worker-image
type: system
title: Linux Tool Worker Image
---
Linux tool worker image provides a deterministic external-tool runtime for preview, scan, and variant jobs.

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
    - poppler-utils
  text_extraction:
    - poppler-utils for pdftotext
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
  virus_scan:
    - clamav
    - clamav-daemon optional
  download_variant:
    - zstd
  metadata_strip:
    - libimage-exiftool-perl optional
clamav:
  - do not bake stale virus database as final source of truth
  - refresh database at runtime or mount managed database volume
  - allow clamd sidecar or in-image clamd deployment
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
