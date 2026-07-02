---
id: data:thumbnail-generation-config
type: data
title: Thumbnail Generation Config
---
Thumbnail generation config controls upload-time image thumbnail creation.

```yaml
fields:
  enabled:
    type: boolean
    default: false
    meaning: create thumbnail data:derived-asset for eligible uploads
  execution_mode:
    enum:
      - sequential
      - async
    default: async
    active_when: enabled true
    meanings:
      sequential:
        - upload wait and final readiness wait until thumbnail work reaches generated, skipped, or failed
        - use when application metadata needs thumbnail object key immediately
      async:
        - original upload completion is enough for wait readiness
        - thumbnail work continues after original upload response
        - status and thumbnail APIs expose pending or placeholder state
  size:
    width: integer default 400
    height: integer default 400
    fit: contain or cover configurable default contain
    upscale: false default
  output:
    lossless_policy:
      enum:
        - force_avif_reduction
        - webp_lossless
      default: force_avif_reduction
      meanings:
        force_avif_reduction: prefer image/avif even when source is visually lossless
        webp_lossless: use image/webp lossless when exact pixel preservation is preferred
    lossy_preferred_content_type: image/avif
    fallback_content_types:
      - image/webp
      - image/jpeg
  source_selection:
    embedded_thumbnail:
      default: prefer_when_available
      applies_to:
        - image containers with EXIF or container thumbnail
        - video containers with cover art or attached picture
        - Office Open XML packages with app thumbnails
        - legacy Office files with summary thumbnails when extractor supports them
      validation:
        - decode extracted bytes with same safety limits as generated thumbnails
        - ignore tiny, corrupted, unsupported, or metadata-only thumbnails
        - re-encode through policy:preview-format-policy before storage
    generated_fallback:
      image: decode full image or first safe frame/page when embedded thumbnail is absent or rejected
      video: use representative keyframe selection from flow:video-preview-generation
      office_document: use system:document-converter fallback when embedded thumbnail is absent or rejected
  supported_source_formats:
    currently_thumbnail_eligible:
      - image/jpeg
      - image/pjpeg
      - image/png
      - image/gif
      - image/webp
      - image/avif
    accepted_but_not_thumbnail_eligible_in_current_server:
      - image/tiff
      - image/bmp
      - image/svg+xml via separate flow:svg-preview-generation
    target_additions:
      high_priority:
        - image/tiff
        - image/heif
        - image/heic
        - image/jp2
        - image/jpx
        - image/jxl
      compatibility_aliases:
        - image/x-tiff
        - image/heif-sequence
        - image/heic-sequence
        - image/jpm
        - image/jpf
  local_processing:
    prefer_go_for_go_decodable_images: true
    cgo_enabled_backends:
      avif: vegidio/avif-go
      webp: chai2010/webp
    pure_go_fallback:
      webp_lossless: eringen/gowebper
      final_static: image/jpeg
  external_processing:
    enabled: false default
    webhook_env: SU_THUMBNAIL_WEBHOOK_URL
    request_timeout: duration configurable
    command: thumbnail-convert subcommand from streamuploader tools image
  video_thumbnail:
    candidate_keyframes:
      type: integer
      default: 10
      env: THUMBNAILS_VIDEO_CANDIDATE_KEYFRAMES
      constraints:
        min: 1
        max: 60
      meaning: number of early video keyframe still candidates to score before choosing representative thumbnail
    score: prefer largest encoded still bytes after normalized scale because it approximates detail and high-frequency content
    overlay: draw centered right-pointing play triangle on stored video thumbnail
  platform_tools:
    macos_sips:
      use_for_when_probed:
        - image/svg+xml after flow:svg-preview-generation sanitize_svg step
        - image/tiff
        - image/heif
        - image/heic
        - image/jp2
        - image/jpx
        - image/jxl when current macOS sips supports it
        - image/vnd.adobe.photoshop
        - image/x-tga
        - application/pdf after flow:document-preview-generation PDF validation as simple first-page fallback
      note: sips support varies by macOS version and installed codecs, so startup probe must decide exact formats
      dx_role:
        - enable easy local preview generation on macOS without installing full converter stack
        - prefer as configured simple fallback after required sanitization or validation
        - expose selected backend so production can detect when sips fallback is used
    svg:
      primary: system:svg-renderer using resvg or rsvg-convert after SVG sanitization
      fallback: sips on macOS after sanitization when configured and probed
    pdf:
      primary: system:document-converter using PDF validation plus Poppler or MuPDF render
      fallback: qlmanage or sips thumbnail after validation when configured and policy allows
  storage:
    object_key_suffix: /thumbnail
    preset_default: default
  startup_logging:
    level: info
    fields:
      - enabled
      - execution_mode
      - size
      - output content type preference
      - selected backend
      - fallback chain
      - external webhook enabled or disabled
references:
  - data:derived-asset
  - flow:image-thumbnail-generation
  - flow:video-preview-generation
  - flow:document-preview-generation
  - policy:preview-format-policy
  - policy:tool-backend-selection-policy
  - system:thumbnail-converter
  - system:media-converter
  - system:document-converter
  - policy:external-delegation-policy
```
