---
id: policy:object-access-policy
type: policy
title: Object Access Policy
---
Object access policy selects one of four download modes for uploaded objects.

```yaml
access_modes:
  direct_public_bucket:
    description: client downloads through public S3 or CDN URL
    authorization:
      - bucket or CDN policy grants read access
      - object_key knowledge is sufficient in demo
    best_for:
      - local demo
      - public assets
      - low API server bandwidth
    constraints:
      - bucket is public or fronted by public read layer
      - no streamuploader authorization per download
      - object key and bucket endpoint are visible
  s3_presigned_download:
    description: backend API asks S3 for short-lived presigned GET URL
    authorization:
      - application backend or backend control caller decides eligibility
      - generated URL is bearer capability until expiry
    best_for:
      - private files with temporary direct S3 delivery
      - reduced API server egress
    constraints:
      - S3 endpoint remains visible
      - audit issuance separately from object access
  api_proxy_download:
    description: streamuploader frontend API streams object bytes from S3
    authorization:
      - config allow_frontend_file_access must be true
      - optional application authorization before exposing URL
    best_for:
      - hiding bucket endpoint
      - download audit
      - uniform browser URL surface
    constraints:
      - API server egress and latency cost
      - range requests and cache headers must be implemented deliberately
      - zip archive generation must enforce count and byte limits
  shared_key_proxy_download:
    description: backend API creates shared key and frontend API resolves it to a target object
    authorization:
      - config enable_shared_key must be true
      - shared key entropy from shared_key_bits
      - shared key record stored in S3 control prefix
    best_for:
      - opaque share links without exposing object key
      - demo of streamuploader-managed access token
      - revocable or expiring object access
    constraints:
      - shared key is bearer capability
      - requires api:shared-key-api and policy:shared-key-policy
      - frontend file access must be enabled for byte serving
default:
  demo_public: direct_public_bucket
  private_low_egress: s3_presigned_download
  private_bucket_hidden: api_proxy_download
  opaque_share: shared_key_proxy_download
required_options:
  allow_frontend_file_access:
    type: bool
    default: false
    affects:
      - api_proxy_download
      - shared_key_proxy_download
  enable_shared_key:
    type: bool
    default: false
    affects:
      - shared_key_proxy_download
  shared_key_bits:
    type: int
    default: 128
    minimum: 96
    recommended: 128
    affects:
      - data:shared-key-record key entropy
  max_archive_files:
    type: int
    default: 100
    affects:
      - api:download-api get_zip_archive
  max_archive_bytes:
    type: int64
    default: 1073741824
    affects:
      - api:download-api get_zip_archive
legacy_mapping:
  public_share: direct_public_bucket
  private_share: s3_presigned_download
  strict_private: api_proxy_download
references:
  - data:file-item
  - data:derived-asset
  - data:shared-key-record
  - policy:storage-key-allocation-policy
  - policy:shared-key-policy
  - api:shared-key-api
  - api:download-api
  - policy:audit-log-policy
```
