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
      - stream to system:s3-storage
      - run policy:file-intake-security
      - update data:file-item state
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
  - flow:session-assembly
  - api:session-progress-api
  - decision:upload-transport-boundary
  - policy:storage-key-allocation-policy
  - api:backend-control-api
  - requirement:application-metadata-submit
```
