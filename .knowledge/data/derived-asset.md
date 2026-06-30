---
id: data:derived-asset
type: data
title: Derived Asset
---
Derived asset represents a generated preview or normalized object linked to an uploaded file item.

```yaml
fields:
  asset_key: opaque unique key
  source_file_key: data:file-item key
  kind:
    enum:
      - image_thumbnail
      - document_preview_page
      - normalized_pdf
      - video_animated_preview
      - compressed_download_variant
      - hls_playlist
      - hls_segment
      - extracted_text
      - extracted_metadata
      - ocr_text
  object_key: S3 object key for generated asset
  content_type: generated media type
  content_encoding: optional HTTP content encoding such as zstd
  replaces_original: boolean optional
  width: integer optional
  height: integer optional
  size_bytes: integer optional
  status:
    enum:
      - pending
      - generated
      - skipped
      - failed
  placeholder:
    cache_control: no-store, no-cache, max-age=0
    returned_while: pending or failed when configured
    mode: s3_placeholder_object or api_generated_placeholder
  error_code: optional
references:
  - data:file-item
  - flow:image-thumbnail-generation
  - flow:svg-preview-generation
  - flow:document-preview-generation
  - flow:video-preview-generation
  - flow:download-variant-generation
  - flow:hls-generation
  - flow:text-extraction-generation
  - flow:ocr-extraction-generation
  - api:derived-asset-serving-api
  - policy:placeholder-serving-policy
  - policy:object-access-policy
```
