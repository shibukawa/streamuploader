---
id: data:shared-key-record
type: data
title: Shared Key Record
---
Shared key record maps an opaque bearer key to a real S3 object key.

```yaml
storage:
  bucket: streamuploader configured bucket
  global_prefix: configured shared_key_prefix, default .streamuploader/shared/
  global_key_path: "{global_prefix}{shared_key}"
  marker_key_path: "{target_object_dir}/.shared/{shared_key}"
  body: optional empty JSON object or compact metadata JSON
  s3_metadata:
    target_object_key: canonical S3 object key
    original_name: optional download file name
    content_type: optional response content type
    created_at: RFC3339 timestamp
    created_by: optional user or backend actor id
    expires_at: optional RFC3339 timestamp
    revoked: optional boolean string
fields:
  shared_key:
    type: URL-safe opaque token
    entropy_bits: policy:object-access-policy shared_key_bits
  target_object_key:
    type: S3 object key
    source: S3 object metadata value
  created_at:
    type: timestamp
  created_by:
    type: optional string
  expires_at:
    type: optional timestamp
    source:
      - backend request expires_at
      - backend request ttl_seconds
      - policy:shared-key-policy configured default_ttl
      - empty when no expiry is configured or requested
  revoked:
    type: bool
rules:
  - key path contains shared_key only, never target_object_key
  - target_object_key lives in object metadata or JSON body
  - global record supports key lookup during /api/file/shared/{shared_key} resolution
  - per-object marker supports cascading delete when target object is deleted
  - one target object may have many shared_key records
  - record and marker objects are control data and must not be exposed as downloadable user content
references:
  - policy:shared-key-policy
  - api:shared-key-api
  - system:s3-storage
```
