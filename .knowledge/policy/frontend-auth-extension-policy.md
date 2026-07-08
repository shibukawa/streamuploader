---
id: policy:frontend-auth-extension-policy
type: policy
title: Frontend Auth Extension Policy
---
Frontend auth extension policy defines how browser-facing API authentication may be customized.

```yaml
scope:
  routes:
    - api:upload-api
    - api:download-api
    - api:shared-key-api
    - api:session-progress-api
  callers:
    - browser user
    - application frontend
    - reverse-proxied application route
go_extension:
  - api:auth-middleware-extension-api frontend middleware wraps public handler
  - custom implementation can accept session cookie, JWT, OAuth proxy headers, mTLS forwarded identity, or application-specific signed request
required_decisions:
  create_upload_key:
    - decide whether caller may create upload key
    - provide tenant_id, upload_owner, allowed prefix policy, and optional role
  upload_content:
    - preserve upload_key capability check
    - optionally bind upload key to data:auth-context upload_owner
  wait_watch_status:
    - verify requested upload keys belong to caller context when auth is present
  frontend_file_access:
    - authorize object read for api_proxy_download and archive download
    - honor policy:object-access-policy mode
  shared_key_access:
    - shared key remains bearer capability unless deployment adds caller binding
constraints:
  - public frontend code must not contain backend secrets
  - anonymous demo mode remains possible when deployment opts in
  - CORS decision remains separate from authentication decision
  - auth middleware must not trust client-supplied prefix without policy:storage-key-allocation-policy validation
  - auth failure must not reveal whether an object key exists
auditing:
  - include subject, tenant_id, route, decision, request id, and reason
  - exclude bearer tokens, cookies, and raw private claims
references:
  - api:auth-middleware-extension-api
  - data:auth-context
  - api:upload-api
  - api:download-api
  - api:shared-key-api
  - api:session-progress-api
  - policy:object-access-policy
  - policy:storage-key-allocation-policy
  - policy:frontend-upload-cancel-policy
  - policy:audit-log-policy
```
