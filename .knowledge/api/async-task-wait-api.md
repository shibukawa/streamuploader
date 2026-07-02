---
id: api:async-task-wait-api
type: api
title: Async Task Wait API
---
Async task wait API lets backend callers block until storage-backed asynchronous processors finish.

```yaml
base_path: /internal
endpoints:
  wait_tasks:
    method: GET
    path: /internal/tasks/wait
    authorization: policy:backend-control-plane-policy
    query:
      object_key:
        type: repeated string or comma-separated list
        required: true
        value: S3 object key
      kind:
        type: repeated string or comma-separated list
        optional: true
        default:
          - image_thumbnail
        examples:
          - image_thumbnail
          - ocr_extraction
          - text_extraction
          - metadata_extraction
      timeout_seconds: integer optional default 60
      poll_millis: integer optional default 200 min 50 max 5000
    behavior:
      - derive deterministic data:async-task-marker object key from kind and object_key
      - poll marker HEAD results until every marker is absent or timeout fires
      - absent marker means no pending asynchronous task for that object and kind
      - marker deletion means terminal success, terminal failure, or task not started
      - return per object and kind pending state
    response:
      ready: boolean
      timeout: boolean
      tasks:
        - object_key: string
          kind: string
          pending: boolean
constraints:
  - no browser CORS
  - no frontend upload token authorization
  - status detail lives in processor result records, not the blocking wait marker
references:
  - data:async-task-marker
  - policy:worker-queue-policy
  - policy:backend-control-plane-policy
  - api:backend-control-api
  - system:s3-storage
```
