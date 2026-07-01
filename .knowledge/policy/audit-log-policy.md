---
id: policy:audit-log-policy
type: policy
title: Audit Log Policy
---
Audit log policy records security and data access decisions for upload, processing, sharing, and download operations.

```yaml
logger:
  implementation: Go log/slog
  sink: stdout
  default_handler: slog.NewTextHandler
  json_handler:
    enabled_by_env:
      - LOG_FORMAT=json
      - SLOG_FORMAT=json
    handler: slog.NewJSONHandler
  level:
    default: info
    env:
      - LOG_LEVEL
  references:
    - data:logging-config
events:
  - upload_key_created
  - upload_started
  - upload_completed
  - security_check_completed
  - virus_scan_completed
  - upload_wait_started
  - upload_wait_completed
  - derived_asset_completed
  - share_created
  - share_resolved
  - share_revoked
  - download_allowed
  - download_denied
  - cleanup_deleted
  - backend_object_delete_requested
  - backend_object_deleted
  - backend_object_rename_requested
  - backend_object_renamed
  - backend_object_replace_requested
  - backend_object_replaced
  - backend_prefix_purged
fields:
  - event_id
  - timestamp
  - actor_subject
  - tenant_id
  - batch_key optional
  - upload_key
  - object_key or display_key
  - old_object_key optional
  - new_object_key optional
  - decision
  - reason_code
  - request_id
  - source_ip optional
storage:
  - stdout structured logs are baseline for container and cloud log collection
  - append-only log sink preferred when deployment needs tamper-resistant audit retention
  - S3 JSONL control prefix acceptable for storage-less deployment
  - redact secrets and bearer tokens
references:
  - policy:object-access-policy
  - policy:jwt-claim-authorization
  - api:backend-control-api
  - policy:backend-control-plane-policy
  - api:share-url-api
  - data:logging-config
```
