---
id: flow:image-thumbnail-generation
type: flow
title: Image Thumbnail Generation
---
Image thumbnail generation creates service-owned derived assets for accepted image uploads.

```yaml
flow:
  trigger: data:file-item uploaded, allowed, and selected by policy:preview-generation-policy
  eligibility:
    - detected content type is allowed image type
    - file item is clean or scan is not required before derivative work
    - image dimensions and pixel count are within configured limits
  steps:
    - name: decode_image
      actions:
        - read from system:s3-storage object or streaming tee when practical
        - decode with safe limits
        - strip untrusted metadata unless explicitly preserved
    - name: generate_variants
      actions:
        - create configured thumbnail sizes
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
references:
  - data:derived-asset
  - data:file-item
  - data:metadata-payload
  - policy:preview-generation-policy
  - policy:preview-format-policy
  - system:s3-storage
```
