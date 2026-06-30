---
id: flow:demo-download-modes
type: flow
title: Demo Download Modes
---
Demo download modes let users experience direct, presigned, proxy, and shared-key delivery for the same uploaded file.

```yaml
mode_selector:
  values:
    - direct_public_bucket
    - s3_presigned_download
    - api_proxy_download
    - shared_key_proxy_download
setup:
  direct_public_bucket:
    requirement:
      - demo bucket or local S3 emulator is public-readable
      - demo app builds public object URL from bucket endpoint and object_key
      - streamuploader is bypassed after upload facts are stored
  s3_presigned_download:
    requirement:
      - demo app or streamuploader backend asks S3 for presigned URL
      - browser redirects to returned URL
  api_proxy_download:
    requirement:
      - streamuploader allow_frontend_file_access true
      - demo app links to /api/file/{key}/content
  shared_key_proxy_download:
    requirement:
      - streamuploader enable_shared_key true
      - streamuploader allow_frontend_file_access true
      - demo app calls /internal/file/shared-keys through backend control path
      - browser downloads through /api/file/shared/{shared_key}/content
steps:
  - user uploads file through api:upload-api
  - demo app stores data:file-item facts
  - user selects download mode in demo UI
  - demo app creates direct public URL itself or asks API for selected non-direct mode
  - browser downloads bytes from S3, presigned S3, proxy API, or shared-key proxy API
future_file_access:
  - thumbnails fit under /api/file/{key}/thumbnail/{preset}
  - derived file variants fit under /api/file/{key}/variants/{variant_key}/content
references:
  - api:download-api
  - api:shared-key-api
  - policy:object-access-policy
  - policy:shared-key-policy
  - data:file-item
```
