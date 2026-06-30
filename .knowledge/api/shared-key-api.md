---
id: api:shared-key-api
type: api
title: Shared Key API
---
Shared Key API lets a backend caller create an opaque key and lets api:download-api resolve it under /api/file.

```yaml
endpoints:
  create_shared_key:
    method: POST
    path: /internal/file/shared-keys
    auth: backend control authorization
    config_gate: enable_shared_key
    body:
      object_key: required S3 object key
      file_name: optional download file name
      content_type: optional response content type
      created_by: optional user or backend actor id
      expires_at: optional timestamp
      ttl_seconds: optional bounded TTL
    response:
      shared_key: opaque key
      download_url: frontend API URL /api/file/shared/{shared_key}/content
      expires_at: optional timestamp
    behavior:
      - generate shared_key with policy:shared-key-policy entropy rules
      - write global data:shared-key-record to S3 control prefix
      - write per-object data:shared-key-record marker to target object directory .shared/{shared_key}
      - return key and URL to backend caller
  revoke_shared_key:
    method: DELETE
    path: /internal/file/shared-keys/{shared_key}
    auth: backend control authorization
    config_gate: enable_shared_key
    behavior:
      - delete global data:shared-key-record
      - delete per-object data:shared-key-record marker when target can be resolved
  revoke_object_shares:
    method: internal
    caller: backend object delete
    behavior:
      - list target object directory .shared/ markers
      - delete markers matching target object
      - delete corresponding global data:shared-key-record objects
  resolve_shared_key:
    method: internal
    caller: api:download-api get_shared_content
    config_gate: enable_shared_key and allow_frontend_file_access
    behavior:
      - load data:shared-key-record from S3 by shared_key path
      - validate expiry and revocation
      - return target_object_key for proxy streaming
errors:
  disabled: 404 or 403 when enable_shared_key is false
  not_found: shared key record missing
  expired: shared key expired
  revoked: shared key revoked
  file_access_disabled: allow_frontend_file_access is false
references:
  - data:shared-key-record
  - policy:shared-key-policy
  - api:download-api
  - policy:backend-control-plane-policy
```
