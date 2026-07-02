---
id: api:backend-control-api
type: api
title: Backend Control API
---
Backend control API exposes privileged storage operations only on the backend listener.

```yaml
listener:
  port: backend_control_port
  mode: same listener or separate listener depending on system:backend-control-listener
  public_exposure: false
  cors: disabled
  intended_callers:
    - system:application-server
    - internal worker
    - operator automation
endpoints:
  delete_object:
    method: DELETE
    path: /internal/objects/{object_key}
    behavior:
      - permanently delete S3 object and selected derived assets according to policy
      - reject capability display keys unless explicitly resolved by backend authorization
      - write audit event before and after deletion
  rename_object:
    method: POST
    path: /internal/objects/{object_key}/rename
    body:
      destination_object_key: string
      if_match_checksum: string optional
      replace_existing: boolean default false
    behavior:
      - implement S3 rename as copy to destination plus delete source after verification
      - preserve or rewrite metadata and tags according to policy
      - never expose this operation to browser clients
  replace_object:
    method: PUT
    path: /internal/objects/{object_key}/content
    body: raw bytes or internal stream
    behavior:
      - overwrite or version object using backend authorization
      - recompute checksum, content type, security status, and derived asset invalidation
      - optionally enqueue re-scan and regeneration jobs
  purge_prefix:
    method: DELETE
    path: /internal/prefixes/{storage_prefix}
    behavior:
      - delete upload work prefix, derived assets, sentinels, and incomplete state
      - require narrow backend permission and audit reason
  invalidate_derived:
    method: POST
    path: /internal/objects/{object_key}/derived/invalidate
    behavior:
      - mark thumbnails, previews, extracted text, OCR, and variants stale
      - enqueue regeneration when requested
  wait_async_tasks:
    method: GET
    path: /internal/tasks/wait
    behavior:
      - block backend caller until selected data:async-task-marker objects disappear
      - support thumbnails, OCR, text extraction, metadata extraction, and future policy:worker-queue-policy task kinds
      - return timeout response with per-task pending state
constraints:
  - no browser CORS
  - no upload capability token authorization
  - require policy:backend-control-plane-policy
  - require idempotency key for mutating operations where safe
references:
  - policy:backend-control-plane-policy
  - system:backend-control-listener
  - api:health-api
  - system:s3-storage
  - policy:audit-log-policy
  - api:async-task-wait-api
  - data:async-task-marker
```
