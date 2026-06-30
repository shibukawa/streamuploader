---
id: system:external-tool-registry
type: system
title: External Tool Registry
---
External tool registry probes installed binaries at startup and selects backends for non-Go media, document, and scan features.

```yaml
startup_probe:
  behavior:
    - check configured absolute paths first
    - search PATH when allowed by deployment policy
    - run version command
    - run optional smoke test per backend
    - publish capability matrix in health/status
  tools:
    ffmpeg:
      commands:
        - ffmpeg -version
        - ffprobe -version
      capabilities:
        - video_preview
        - hls_generation
        - media_metadata_strip
    avconvert:
      platform: macOS
      command: avconvert -h
      capabilities:
        - video_preview fallback
        - simple transcoding fallback
    sips:
      platform: macOS
      command: sips --help
      capabilities:
        - image_thumbnail fallback
        - image_metadata_strip fallback
    qlmanage:
      platform: macOS
      command: qlmanage -h
      capabilities:
        - generic_thumbnail fallback
      note: Quick Look output depends on host plugins and OS version
    libreoffice:
      command: soffice --headless --version
      capabilities:
        - office_to_pdf
        - office_text_export fallback
    pdf_renderer:
      candidates:
        - pdftoppm
        - mutool
      capabilities:
        - pdf_page_render
    pdftotext:
      command: pdftotext -v
      capabilities:
        - pdf_text_extraction
    exiftool:
      command: exiftool -ver
      capabilities:
        - metadata_extraction
    tika:
      optional: true
      command: tika --version
      capabilities:
        - broad_text_extraction
        - broad_metadata_extraction
    tesseract:
      command: tesseract --version
      capabilities:
        - ocr_text_extraction
    rsvg_convert:
      command: rsvg-convert --version
      capabilities:
        - svg_rasterize
    inkscape:
      command: inkscape --version
      capabilities:
        - svg_rasterize high_fidelity_fallback
    clamav:
      candidates:
        - clamdscan
        - clamscan
      capabilities:
        - virus_scan
    zstd:
      command: zstd --version
      capabilities:
        - download_variant_compression
selection:
  - choose highest priority available backend per capability
  - disable feature when required backend is missing and policy marks it optional
  - fail startup only for backends marked required by deployment configuration
security:
  - prefer absolute configured paths in production
  - isolate worker processes
  - pass arguments as argv, not shell strings
  - enforce timeout, memory, CPU, input, and output limits
references:
  - policy:tool-backend-selection-policy
  - system:linux-tool-worker-image
  - system:text-extractor
  - system:ocr-engine
```
