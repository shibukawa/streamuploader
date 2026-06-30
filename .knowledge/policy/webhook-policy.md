---
id: policy:webhook-policy
type: policy
title: Webhook Policy
---
Webhook policy notifies external systems about upload, processing, failure, and cleanup events.

```yaml
events:
  - upload_completed
  - upload_wait_completed
  - preview_completed
  - scan_failed
  - derived_asset_failed
  - cleanup_completed
  - share_revoked
delivery:
  - signed payload
  - idempotency key
  - retry with backoff
  - dead letter queue after terminal failure
payload:
  - event_id
  - event_type
  - batch_key optional
  - upload_key optional
  - object_key or display_key optional
  - status
references:
  - policy:worker-queue-policy
  - policy:audit-log-policy
```
