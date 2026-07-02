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
  - policy:preview-format-policy
  - policy:tool-backend-selection-policy
  - system:thumbnail-converter
  - policy:external-delegation-policy
```
