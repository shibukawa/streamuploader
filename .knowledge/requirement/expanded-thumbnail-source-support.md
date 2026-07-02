---
id: requirement:expanded-thumbnail-source-support
type: requirement
title: Expanded Thumbnail Source Support
---
Thumbnail support should cover common modern image formats and extract existing representative images before expensive rendering.

```yaml
requirement:
  scope:
    - static image thumbnails from more source formats
    - embedded thumbnail extraction from images, videos, and Office documents
    - generated fallback when extraction is absent, invalid, or unsafe
    - video still thumbnail with play overlay
    - configurable number of video keyframes scored for still thumbnail selection
  current_observed_gap:
    implementation:
      allowed_image_category:
        - image/png
        - image/jpeg
        - image/gif
        - image/webp
        - image/avif
        - image/tiff
        - image/bmp
        - image/svg+xml
      thumbnail_eligible_server_types:
        - image/jpeg
        - image/pjpeg
        - image/png
        - image/gif
        - image/webp
        - image/avif
      not_currently_listed:
        - image/heif
        - image/heic
        - image/jxl
        - image/jp2
        - image/jpx
    knowledge:
      - data:thumbnail-generation-config did not define embedded thumbnail extraction
      - flow:video-preview-generation described animated preview, not still thumbnail selection
  target_source_priority:
    image:
      - validate and use embedded thumbnail when present and good enough
      - generate thumbnail from decoded full image when no usable embedded thumbnail exists
    video:
      - use attached picture or cover art when present and safe
      - otherwise select representative keyframe from early candidates
      - overlay play indicator on stored still thumbnail
    office_document:
      - use OOXML package thumbnail or legacy Office summary thumbnail when present and safe
      - otherwise convert through system:document-converter
  image_format_priority:
    add_first:
      - TIFF because accepted today but not thumbnail eligible
      - BMP because accepted today but not thumbnail eligible
      - HEIF/HEIC because common from Apple devices
      - JPEG 2000 because used by archival and professional systems
      - JPEG XL because emerging modern still format
      - PSD when platform or isolated tool can flatten safely
      - TGA when platform or ffmpeg can decode safely
    consider_later:
      - camera RAW only behind explicit opt-in due to decode cost and metadata risk
      - AI only through isolated external tools due to complexity
  backend_preferences:
    macos_sips:
      - use when startup probe confirms source decode and requested output conversion
      - good candidates include SVG fallback, JPEG XL, JPEG 2000, HEIC, HEIF, TIFF, PDF first-page fallback, PSD, and TGA
      - do not assume every macOS version supports every format
      - keep enabled as a low-friction local development fallback after required sanitization or validation
    ffmpeg:
      - use for image or video formats not handled by Go or sips when decode and encoder support are probed
      - useful for TGA, some TIFF variants, video still extraction, and broad fallback decoding
    svg:
      - use flow:svg-preview-generation with system:svg-renderer, preferably resvg or rsvg-convert
      - allow sips fallback only after sanitize_svg rejects active content and external references
      - never rasterize unsanitized SVG through generic image fallback
    pdf:
      - use flow:document-preview-generation with system:document-converter
      - validate PDF safety first
      - render first configured page through Poppler or MuPDF
      - use sips or Quick Look as optional macOS fallback for simple thumbnails after validation
  acceptance:
    - unsupported decode never rejects otherwise accepted original upload unless policy marks thumbnail required
    - generated data:derived-asset records source method: embedded, generated_full_decode, generated_video_keyframe, or generated_document_render
    - data:thumbnail-generation-config controls video keyframe candidate count
    - extractor output is re-encoded to approved preview format, not served directly
    - dangerous metadata and active content are stripped
    - sips fallback never bypasses SVG sanitization or PDF validation
relations:
  - data:thumbnail-generation-config
  - flow:image-thumbnail-generation
  - flow:video-preview-generation
  - flow:document-preview-generation
  - policy:preview-generation-policy
  - policy:preview-format-policy
  - system:thumbnail-converter
  - system:media-converter
  - system:document-converter
```
