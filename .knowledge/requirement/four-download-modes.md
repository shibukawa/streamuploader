---
id: requirement:four-download-modes
type: requirement
title: Four Download Modes
---
Streamuploader must provide sample-ready support for four object download modes.

```yaml
required_modes:
  direct_public_bucket:
    summary: public bucket URL download for demo
    implementation_owner: demo app
    streamuploader_role: none for URL creation and byte serving
  s3_presigned_download:
    summary: backend asks S3 for presigned URL and returns it
    implementation_owner: streamuploader backend API or demo app using S3 client
  api_proxy_download:
    summary: frontend API streams S3 object and hides bucket
    implementation_owner: streamuploader
  shared_key_proxy_download:
    summary: backend API creates shared key, frontend API resolves it and streams object
    implementation_owner: streamuploader
streamuploader_options:
  enable_shared_key:
    required: true
    default: false
  shared_key_bits:
    required: true
    default: 128
    minimum: 96
  allow_frontend_file_access:
    required: true
    default: false
  max_archive_files:
    required: false
    default: 100
  max_archive_bytes:
    required: false
    default: 1073741824
demo_requirements:
  - expose all four modes in UI
  - show current mode and resulting URL class
  - support direct mode with public local bucket policy
  - support presigned mode without public bucket
  - support proxy mode with hidden bucket endpoint
  - support shared key mode with backend-created key
  - optionally support multi-file zip download through /api/files/{key},{key}
acceptance:
  - same uploaded file can be downloaded through each mode
  - direct URL is built by demo app and contains S3 or CDN endpoint
  - direct access does not call streamuploader file API
  - presigned URL contains S3 signature parameters
  - proxy URL uses /api/file/{key}/content and contains object key
  - shared-key URL uses /api/file/shared/{shared_key}/content and contains shared_key only
  - zip URL uses /api/files/{key},{key} and returns application/zip
  - upload API remains under /api/upload and does not serve file bytes
  - future thumbnails and variants can extend /api/file without changing upload routes
references:
  - flow:demo-download-modes
  - api:download-api
  - api:shared-key-api
  - policy:object-access-policy
  - policy:shared-key-policy
```
