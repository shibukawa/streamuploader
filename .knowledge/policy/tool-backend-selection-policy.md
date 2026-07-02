---
id: policy:tool-backend-selection-policy
type: policy
title: Tool Backend Selection Policy
---
Tool backend selection policy maps features to available external tools with fallback and degradation rules.

```yaml
features:
  video_preview:
    preferred: ffmpeg
    fallback:
      - avconvert on macOS for simple preview when ffmpeg is unavailable
    missing_behavior: skip preview or mark failed depending on feature requirement
  hls_generation:
    preferred: ffmpeg
    fallback: none
    missing_behavior: disable HLS
  office_preview:
    preferred: libreoffice plus pdf_renderer
    fallback:
      - qlmanage on macOS for simple thumbnail fallback after validation when configured
      - sips on macOS for first-page PDF thumbnail fallback after validation when configured and probed
    missing_behavior: skip document preview
  svg_preview:
    preferred: resvg
    fallback:
      - rsvg-convert
      - resvg
      - inkscape for high fidelity
      - sips on macOS for simple fallback after sanitization when configured and probed
      - qlmanage on macOS for environment-dependent fallback
    missing_behavior: skip SVG preview
  image_thumbnail:
    preferred:
      - system:thumbnail-converter in-process Go backend when source is Go-decodable and codec is linked
      - vegidio/avif-go for AVIF when CGO is enabled and selected
      - chai2010/webp for WebP when CGO is enabled and selected
    fallback:
      - sips on macOS for AVIF-capable simple transforms
      - sips on macOS for probed SVG, JPEG XL, JPEG 2000, HEIC, HEIF, TIFF, PDF thumbnail fallback, PSD, and TGA support
      - vips for fast resize and AVIF/WebP output
      - ffmpeg for broad decode fallback
      - eringen/gowebper for pure-Go lossless/simple WebP when CGO is disabled
      - image/jpeg final static fallback
    missing_behavior: skip image preview or use original only when policy allows
  virus_scan:
    preferred: clamdscan
    fallback:
      - clamscan
    missing_behavior: disable when optional, fail commit when policy requires scan
  download_variant:
    preferred:
      - Go zstd library
      - zstd CLI
    missing_behavior: skip compressed variant
  text_extraction:
    preferred:
      - direct parser for text-like files
      - docx parser for OOXML
      - Go PDF text parser when quality is acceptable
      - Go metadata or EXIF parser for allowlisted fields
    fallback:
      - pdftotext for PDF embedded text
      - Apache Tika when enabled
      - LibreOffice text export fallback
      - local command processor for document AI when policy explicitly delegates
    missing_behavior: skip extracted_text or mark failed depending on requirement
  metadata_extraction:
    preferred:
      - native Go parser when available
      - Go EXIF parser for image metadata
      - OOXML core properties parser for Office metadata
    fallback:
      - exiftool for broad or difficult metadata formats
      - Apache Tika when enabled
    missing_behavior: skip extracted_metadata
  ocr_extraction:
    preferred: tesseract
    fallback:
      - local command processor that calls cloud OCR when explicitly configured
      - local command processor that calls document AI when policy explicitly delegates
    missing_behavior: skip OCR or mark failed depending on requirement
reporting:
  - expose selected backend in health API
  - include backend name and version in worker audit event
  - include missing backend reason in status when feature is skipped
references:
  - system:external-tool-registry
  - system:thumbnail-converter
  - system:linux-tool-worker-image
  - system:text-extractor
  - system:ocr-engine
  - system:external-processing-delegates
  - system:local-command-processor
  - policy:external-delegation-policy
  - policy:processor-execution-policy
  - policy:worker-queue-policy
```
