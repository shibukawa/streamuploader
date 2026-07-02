---
id: data:async-task-marker
type: data
title: Async Task Marker
---
Async task marker is a small S3 object used as durable pending state for post-accept processors.

```yaml
storage:
  prefix: .streamuploader/tasks/
  key_derivation:
    input:
      - kind
      - object_key
    algorithm: sha256(kind + NUL + object_key)
    suffix: .json
fields:
  object_key: source object key
  kind: processor kind from policy:worker-queue-policy
  status: queued or running
  created_at: RFC3339Nano
  updated_at: RFC3339Nano
lifecycle:
  create:
    - when asynchronous processor starts
    - before backend-visible pending work can outlive the request
  delete:
    - after processor reaches terminal success
    - after processor reaches terminal failure
    - during stale work cleanup when task owner decides work is abandoned
wait_semantics:
  marker_exists: pending
  marker_absent: complete or not scheduled
future_task_kinds:
  - ocr_extraction
  - text_extraction
  - metadata_extraction
  - download_variant
references:
  - api:async-task-wait-api
  - policy:worker-queue-policy
  - policy:work-sentinel-cleanup
  - data:processor-result
```
