---
id: policy:backend-control-plane-policy
type: policy
title: Backend Control Plane Policy
---
Backend control plane policy protects destructive and privileged storage operations from browser-facing APIs.

```yaml
separation:
  - browser-facing API accepts file content and upload status only
  - backend control API owns destructive and key mutation operations
  - backend routes are not mounted on public upload listener
  - CORS is disabled on backend listener
authentication:
  preferred:
    - mTLS between application server and streamuploader
    - service account JWT with audience restricted to backend listener
    - private network plus signed internal request
  forbidden:
    - upload capability token
    - browser session cookie alone
    - public API key in frontend code
authorization:
  permissions:
    - object:delete
    - object:rename
    - object:replace
    - prefix:purge
    - derived:invalidate
  checks:
    - tenant ownership
    - object key namespace
    - object checksum or ETag when supplied
    - reason code for destructive actions
    - optional two-person or operator approval for bulk purge
s3_operations:
  delete:
    - delete canonical object, selected derived assets, and control markers according to request scope
    - avoid deleting shared physical object while checksum dedupe refcount is nonzero
  rename:
    - S3 has no atomic rename
    - copy destination, verify checksum or ETag, update metadata references, then delete source
    - treat operation as idempotent state machine when possible
  replace:
    - write new object body
    - recompute content facts
    - invalidate stale derived assets and extracted content
observability:
  - audit every request, decision, S3 operation, and failure
  - include actor, service account, tenant, object key, old key, new key, checksum, request id, and reason
  - expose no secret or bearer token in logs
references:
  - api:backend-control-api
  - system:backend-control-listener
  - policy:audit-log-policy
  - policy:checksum-dedupe-policy
```
