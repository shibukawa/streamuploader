---
id: flow:image-thumbnail-generation
type: flow
title: Image Thumbnail Generation
---
Image thumbnail generation creates service-owned derived assets for accepted image uploads.

```yaml
flow:
  trigger: data:file-item uploaded, allowed, data:thumbnail-generation-config enabled true, and selected by policy:preview-generation-policy
  eligibility:
    - detected content type is allowed image type
    - file item is clean or scan is not required before derivative work
    - image dimensions and pixel count are within configured limits
    - data:thumbnail-generation-config size and output policy are valid
    - source format is thumbnail eligible or delegated to a safe extractor
  source_formats:
    currently_eligible:
      - image/jpeg
      - image/pjpeg
      - image/png
      - image/gif
      - image/webp
      - image/avif
    target_eligible:
      - image/tiff
      - image/bmp
      - image/heif
      - image/heic
      - image/jp2
      - image/jpx
      - image/jxl
  steps:
    - name: create_pending_asset
      actions:
        - allocate image_thumbnail data:derived-asset with object_key source object key plus /thumbnail
        - expose pending status for async mode
    - name: try_embedded_thumbnail
      actions:
        - inspect EXIF or container thumbnail without trusting metadata
        - decode extracted thumbnail bytes under same byte, pixel, and timeout limits
        - reject embedded thumbnail when too small, corrupt, wrong aspect, or unsupported
        - re-encode accepted embedded thumbnail through policy:preview-format-policy
        - skip full-image decode when embedded thumbnail satisfies configured size and quality threshold
    - name: decode_image
      actions:
        - read from system:s3-storage object or streaming tee through io.MultiWriter when practical
        - decode with safe limits
        - use system:thumbnail-converter backend selected at startup
        - strip untrusted metadata unless explicitly preserved
    - name: generate_variants
      actions:
        - create configured thumbnail sizes, default 400x400
        - choose output format using policy:preview-format-policy
        - avoid upscaling beyond source dimensions
    - name: store_assets
      actions:
        - write generated files to system:s3-storage
        - record data:derived-asset entries on data:file-item
    - name: expose_in_payload
      actions:
        - include generated asset keys in data:metadata-payload when required or available
  failure:
    optional_thumbnail:
      - keep file upload accepted
      - mark thumbnail failed or skipped
    required_thumbnail:
      - block ready or commit until generated
      - return actionable status
  execution:
    sequential:
      - upload wait waits for generated, skipped, or failed thumbnail terminal state
    async:
      - upload wait ignores thumbnail completion after original upload is accepted
      - api:derived-asset-serving-api may return pending placeholder
references:
  - data:derived-asset
  - data:file-item
  - data:metadata-payload
  - data:thumbnail-generation-config
  - requirement:expanded-thumbnail-source-support
  - policy:preview-generation-policy
  - policy:preview-format-policy
  - system:thumbnail-converter
  - system:s3-storage
```
