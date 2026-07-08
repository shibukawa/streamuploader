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
  owner: policy:backend-auth-extension-policy
  core_behavior:
    - backend control plane delegates authentication to api:auth-middleware-extension-api backend middleware
    - core does not include BackendAuthToken bearer authorization
    - core does not define mTLS, service account JWT, or private network signed request as sufficient by itself
    - default backend auth middleware passes through without app-level authentication
  external_boundary:
    - production deployments may rely on security groups, firewall rules, API Gateway, ingress policy, service mesh, private listener placement, or equivalent controls
    - streamuploader treats those controls as deployment architecture, not application auth logic
    - deployment owner decides whether external boundary is sufficient for backend control exposure
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
    - extracted-content:read
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
  extracted_content_read:
    - read only the derived .text.json artifact for an authorized source object
    - deny when caller lacks extracted-content read permission for tenant, object namespace, or privacy classification
    - audit because extracted text may expose full private file contents
observability:
  - audit every request, decision, S3 operation, and failure
  - include actor, backend actor, tenant, object key, old key, new key, checksum, request id, and reason
  - expose no secret or bearer token in logs
references:
  - api:backend-control-api
  - api:extracted-content-api
  - system:backend-control-listener
  - api:auth-middleware-extension-api
  - policy:backend-auth-extension-policy
  - policy:audit-log-policy
  - policy:checksum-dedupe-policy
```
