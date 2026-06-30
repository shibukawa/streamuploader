---
id: concept:upload-experience-improvements
type: concept
title: Upload Experience Improvements
---
Upload experience improves when the API exposes resumability, progress, service-mediated efficient transfer, and clear readiness state.

```yaml
improvements:
  resumable_upload:
    value: resume large files after network failure
    options:
      - tus-like protocol
      - multipart part retry
      - client-side chunk manifest
  immediate_upload:
    value: reduce final submit wait by starting transfer on drag and drop or file selection
    options:
      - create upload keys lazily on first file selection
      - allocate capability-style folder and object key immediately
      - upload files while user edits metadata
      - correlate upload_key with client temporary id
  progress_visibility:
    value: show per-file upload progress, background work, and upload batch readiness
    options:
      - status endpoint
      - websocket watch channel for dynamic upload_key sets
      - blocking wait endpoint for simple submit flows
  service_mediated_s3_transfer:
    value: keep file checks while using efficient S3 transfer internally
    options:
      - AWS SDK service-side multipart upload
      - bounded memory streaming from request body to S3 parts
      - serverless nodes recover state from S3 .work sentinel
  upload_policy_display:
    value: inform client UI without blocking immediate transfer goal
    options:
      - session policy response
      - allowed media types
      - max file count and size
  background_processing:
    value: avoid long request waits
    options:
      - async virus scan
      - image thumbnail, SVG raster preview, document preview, and video animated preview generation
      - text, metadata, and OCR extraction for search
      - configurable pre_accept, post_accept, and on_demand processors
      - local command processors for APIs such as Vision, translation, AWS, GCP, or internal services
      - post-acceptance worker
      - no-cache placeholder for pending derived assets
      - S3-stored or API-generated placeholder
  image_preview:
    value: let clients show uploaded images before or after metadata submit
    options:
      - generated thumbnails
      - AVIF for lossy photo-like previews
      - WebP for lossless graphic previews
      - per-file derived asset status
      - configurable required or optional thumbnail policy
  svg_preview:
    value: let clients preview SVG safely without rendering untrusted source directly
    options:
      - sandboxed Inkscape rasterization
      - active content rejection
      - lossless WebP output
  document_preview:
    value: let clients preview PDFs and Office files without downloading originals
    options:
      - PDF page thumbnails
      - Office to PDF conversion
      - first page rendered preview
  search_extraction:
    value: prepare uploaded files for search engines or application metadata without hardcoding every provider
    options:
      - extracted text from Word, PDF, HTML, text, JSON, and CSV
      - EXIF and document metadata extraction with allowlist or denylist
      - opt-in OCR for images and image-only PDFs
      - normalized UTF-8 text and chunk descriptors
      - data:processor-result JSON merged into configured metadata paths
  video_preview:
    value: let clients inspect videos without downloading originals
    options:
      - ffmpeg animated preview
      - short muted clip
      - animated WebP, GIF, or APNG output
  cleanup:
    value: control abandoned sessions without separate durable storage
    options:
      - S3 .work sentinel object
      - session prefix sweeper
      - expiry grace period
  customizable_keys:
    value: support capability-style sharing and sender controlled grouping
    options:
      - cryptographic random base62 folder token
      - global namespace with high entropy token
      - user or sender prefix plus random token
      - folder plus file name returned to metadata form
  download_variants:
    value: reduce repeated download bandwidth with background generated variants
    options:
      - zstd precompressed variant object
      - Content-Encoding on variant only
      - optional original replacement mode for storage savings
      - serve by Accept-Encoding
      - skip already compressed media
  share_and_audit:
    value: support revocable share URLs and access traceability
    options:
      - API-level short URL alias
      - key rotation and revocation
      - audit log for access and deny events
  processing_reliability:
    value: make expensive workers observable and retryable
    options:
      - queue with retry and dead letter
      - webhook failure hooks
      - status surface for failed terminal jobs
  dedupe_and_privacy:
    value: reduce storage and privacy leaks
    options:
      - checksum dedupe with ref counts
      - EXIF and metadata stripping
      - cautious delete after ref count reaches zero
  advanced_delivery:
    value: improve large media downloads and playback
    options:
      - API proxy byte range
      - HLS generation for video
references:
  - api:upload-api
  - api:session-progress-api
  - flow:session-assembly
  - policy:processor-execution-policy
  - system:local-command-processor
  - flow:drag-drop-immediate-upload
  - decision:upload-transport-boundary
  - decision:serverless-upload-state
  - flow:image-thumbnail-generation
  - flow:svg-preview-generation
  - flow:document-preview-generation
  - flow:video-preview-generation
  - policy:preview-generation-policy
  - policy:preview-format-policy
  - policy:search-extraction-policy
  - flow:text-extraction-generation
  - flow:ocr-extraction-generation
  - policy:work-sentinel-cleanup
  - policy:storage-key-allocation-policy
  - policy:download-variant-policy
  - flow:download-variant-generation
  - flow:security-gated-upload-acceptance
  - api:derived-asset-serving-api
  - policy:placeholder-serving-policy
  - policy:object-access-policy
  - policy:jwt-claim-authorization
  - api:share-url-api
  - api:download-api
  - policy:share-url-policy
  - policy:audit-log-policy
  - policy:worker-queue-policy
  - policy:checksum-dedupe-policy
  - policy:metadata-stripping-policy
  - policy:webhook-policy
  - flow:hls-generation
```
