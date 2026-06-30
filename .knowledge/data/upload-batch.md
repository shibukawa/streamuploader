---
id: data:upload-batch
type: data
title: Upload Batch
---
Upload batch is an internal or request-scoped view over upload keys used to wait for multiple files.

```yaml
fields:
  batch_key: optional opaque batch key or server-side wait id
  status:
    enum:
      - created
      - receiving_files
      - ready
      - failed
      - expired
  files:
    type: list of data:file-item
  expected_files:
    min_count: configurable
    max_count: configurable
    required_roles: configurable
  expires_at: timestamp
  progress:
    total_files: integer
    uploaded_files: integer
    total_bytes: integer optional
    uploaded_bytes: integer optional
    current_events:
      type: list of recent progress event ids
  work_sentinel_key: S3 .work object key
removed:
  - no application metadata draft is stored
  - no upstream record id is stored
  - no commit idempotency key is needed
references:
  - api:upload-api
  - api:session-progress-api
  - flow:session-assembly
  - policy:work-sentinel-cleanup
```
