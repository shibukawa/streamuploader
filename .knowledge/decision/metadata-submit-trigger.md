---
id: decision:metadata-submit-trigger
type: decision
title: Metadata Submit Trigger
---
Initial API should not commit application metadata; it only waits for upload completion.

```yaml
decision:
  selected: client_metadata_submit_after_wait
  rationale:
    - streamuploader should solve upload transport and storage facts only
    - application server owns form schema, login, authorization, and metadata persistence
    - frontend can wait for file uploads, then submit metadata plus file facts in one normal app request
    - avoids duplicate application API contracts inside streamuploader
  progress_modes:
    websocket_watch:
      - client can add upload_keys to watched set over an existing connection
      - server returns snapshots and frequent progress/state updates for watched keys
      - terminal state contains final file facts when available
    blocking:
      - hold HTTP response until all requested upload_keys complete or timeout
      - return file facts as JSON
  removed:
    - streamuploader finalization endpoint for application metadata
    - streamuploader auto-submit of application metadata
    - streamuploader-owned idempotent application metadata retry
references:
  - flow:session-assembly
  - api:upload-api
  - requirement:application-metadata-submit
```
