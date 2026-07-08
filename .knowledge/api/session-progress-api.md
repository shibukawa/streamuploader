---
id: api:session-progress-api
type: api
title: Session Progress API
---
Session progress API exposes upload key status through polling and a WebSocket watch channel.

```yaml
endpoints:
  status:
    method: GET
    path: "{api:upload-api base_path}/keys/{upload_key}"
    response: data:file-item
  watch_websocket:
    method: GET
    path: "{api:upload-api base_path}/watch"
    protocol: websocket
    client_messages:
      watch:
        type: watch
        upload_keys: list of upload_key
        behavior:
          - add keys to watched set
          - idempotent for already watched keys
      unwatch:
        type: unwatch
        upload_keys: list of upload_key
        behavior:
          - remove keys from watched set
      ping:
        type: ping
        client_id: optional string
    server_messages:
      snapshot:
        type: snapshot
        upload_key: upload_key
        item: data:file-item
        sent_when:
          - key is first watched
          - client reconnects and watches again
      progress:
        type: progress
        upload_key: upload_key
        uploaded_bytes: integer
        size_bytes: integer optional
        status: uploading
        display_rule:
          - when post-upload synchronous security gate is pending, uploaded_bytes must be capped below full size, default 98 percent
          - only terminal uploaded state may expose full byte completion for gated uploads
      state:
        type: state
        upload_key: upload_key
        status: key_created, uploaded, clean, rejected, failed, expired
        item: data:file-item optional when final facts are available
      error:
        type: error
        upload_key: upload_key optional
        code: string
        message: string
behavior:
  - polling status is the source of truth
  - WebSocket watch is used when browser should receive frequent updates while the watched upload key set changes
  - wait endpoint remains blocking JSON for simple forms
  - client can add upload keys to an existing WebSocket without reopening the connection
  - server should send an initial snapshot for every newly watched key
  - server should send terminal state once and keep it available through polling status
  - browser progress bars must not turn completed or success-colored solely because request bytes reached storage while synchronous scan or staging gates are still pending
references:
  - data:upload-batch
  - data:file-item
  - flow:drag-drop-immediate-upload
```
