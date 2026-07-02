---
id: policy:frontend-upload-cancel-policy
type: policy
title: Frontend Upload Cancel Policy
---
Frontend upload cancel policy permits browser cancellation only before file bytes are sent.

```yaml
scope:
  allowed_state: key_created
  denied_states:
    - uploading
    - uploaded
    - failed
    - expired
    - canceled
owner_binding:
  mechanism: opaque HttpOnly cookie
  cookie_name: streamuploader_owner
  stored_value: sha256(cookie value)
  stored_places:
    - in-memory data:file-item server state
    - upload deadline marker from policy:upload-key-deadline-policy
  reason:
    - IP address is unstable behind NAT, proxies, mobile networks, and IPv6 privacy rotation
    - cookie binding follows the browser that requested upload key issuance
api_behavior:
  cancel:
    method: DELETE
    path: /api/upload/keys/{upload_key}
    success_status: 204
    errors:
      owner_mismatch: 403
      upload_already_started: 409
      upload_key_expired: 410
side_effects:
  - delete upload deadline marker
  - set upload state to canceled
  - notify websocket watchers
  - do not delete uploaded object because uploaded state cannot be canceled through this API
references:
  - api:upload-api
  - data:file-item
  - policy:upload-key-deadline-policy
```
