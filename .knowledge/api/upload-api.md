---
id: api:upload-api
type: api
title: Upload API
---
Upload API issues upload keys, accepts file bytes, and lets clients watch upload progress before submitting application metadata elsewhere.

```yaml
base_path:
  default: /api/upload
  configurable: true
endpoints:
  create_upload_key:
    method: POST
    path: "{base_path}/keys"
    body:
      file_name: string
      content_type: string optional
      size_bytes: integer optional
      role: string optional
      prefix: string required only when policy:storage-key-allocation-policy requires caller prefix
      key_namespace: global or user_prefixed or custom_prefixed optional
    response:
      upload_key: opaque unique key
      expires_at: timestamp
      upload_url: service content endpoint, not client-visible S3 URL
      storage_prefix: generated folder prefix
      object_key: generated folder plus safe file name
      display_key: key returned for metadata payload
  upload_file:
    method: PUT
    path: "{base_path}/keys/{upload_key}/content"
    behavior:
      - receive bytes through service boundary
      - inspect prefix before storage commit
      - reject declared MIME and magic-header mismatch with JSON error
      - stream to system:s3-storage
      - run policy:file-intake-security
      - update data:file-item state
    errors:
      content_type_mismatch:
        status: 415
        body:
          error: content_type_mismatch
          message: declared content type does not match detected content type
      detected_type_unknown:
        status: 415
        body:
          error: detected_type_unknown
          message: uploaded content type could not be detected
      script_upload_rejected:
        status: 415
        body:
          error: script_upload_rejected
          message: detected script family, expected script MIME, and opt-in setting hint
      content_type_denied:
        status: 415
        body:
          error: content_type_denied
          message: uploaded content type is denied
      content_type_not_allowed:
        status: 415
        body:
          error: content_type_not_allowed
          message: uploaded content type is not allowed
      prefix_read_failed:
        status: 400
        body:
          error: prefix_read_failed
          message: uploaded content prefix could not be read
  wait_uploads:
    method: POST
    path: "{base_path}/wait"
    body:
      upload_keys: list of upload_key
      timeout_seconds: optional
    behavior:
      - verify requested upload keys exist and belong to current caller context when auth is delegated
      - block until all requested uploads reach terminal success or failure or timeout
      - return durable file facts for frontend metadata submission
  watch_uploads:
    method: GET
    path: "{base_path}/watch"
    protocol: websocket
    behavior:
      - client opens a bidirectional status channel
      - client sends upload_key additions at any time
      - server returns current status immediately for each watched key
      - server keeps sending progress and terminal state updates for watched keys
      - final file facts are included when an upload key reaches uploaded or clean state
  status:
    method: GET
    path: "{base_path}/keys/{upload_key}"
    response: data:file-item
removed:
  - streamuploader does not accept application metadata
  - streamuploader does not submit or forward metadata to system:application-server
  - streamuploader does not expose client-visible S3 presigned upload URLs
  - streamuploader public API does not expose object delete, rename, replace, or purge operations
  - wait endpoint does not stream progress
references:
  - data:file-item
  - data:upload-batch
  - data:storage-key-allocation
  - data:security-check-result
  - flow:session-assembly
  - flow:security-gated-upload-acceptance
  - api:session-progress-api
  - decision:upload-transport-boundary
  - decision:mime-detector-library
  - policy:storage-key-allocation-policy
  - policy:file-intake-security
  - api:backend-control-api
  - requirement:application-metadata-submit
```
