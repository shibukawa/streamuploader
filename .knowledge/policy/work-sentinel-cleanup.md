---
id: policy:work-sentinel-cleanup
type: policy
title: Work Sentinel Cleanup
---
Work sentinel cleanup stores upload key work state in S3 object prefixes without a separate database.

```yaml
policy:
  goal: storage-less upload cleanup using S3-compatible objects only
  prefix_layout:
    upload_prefix: uploads/{upload_key}/
    sentinel: uploads/{upload_key}/.work
    file: uploads/{upload_key}/file
    derived: uploads/{upload_key}/derived/{asset_key}
    final_marker: uploads/{upload_key}/.ready optional
  sentinel_object:
    name: .work
    content_type: application/json
    fields:
      - upload_key
      - created_at
      - updated_at
      - expires_at
      - status
      - storage_allocations
      - multipart_upload_ids optional
      - part_etags optional
      - progress_summary
  cleanup_rule:
    - list prefixes containing .work
    - if .work exists and expires_at or last_modified is older than threshold, delete prefix objects
    - skip prefixes with .ready unless retention policy says otherwise
    - tolerate eventual consistency by using grace period
  ready_behavior:
    - remove .work or replace with .ready after upload facts are durable
    - cleanup orphaned uploaded objects when application metadata is never submitted and retention policy allows it
  constraints:
    - no local filesystem required for durable state
    - no separate database required for cleanup
    - no in-memory-only upload state
    - S3 upload uses service-side multipart upload, not presigned URLs
    - cleanup worker must be idempotent
references:
  - system:s3-storage
  - data:upload-batch
  - decision:serverless-upload-state
```
