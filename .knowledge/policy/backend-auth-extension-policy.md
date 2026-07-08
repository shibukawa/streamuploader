---
id: policy:backend-auth-extension-policy
type: policy
title: Backend Auth Extension Policy
---
Backend auth extension policy defines replaceable authentication for privileged backend APIs.

```yaml
scope:
  routes:
    - api:backend-control-api
    - api:shared-key-api backend operations
    - api:extracted-content-api backend operations
    - api:async-task-wait-api backend operations
  callers:
    - application server
    - internal worker
    - operator automation
go_extension:
  - api:auth-middleware-extension-api backend middleware wraps backend handler
  - custom implementation owns the complete backend authentication decision
  - mTLS, service account JWT, private network signed request, OAuth client credentials, and gateway assertion are possible inputs, not core defaults or recommendations
default:
  - core provides no BackendAuthToken compatibility path
  - core provides no mTLS verifier
  - core backend middleware passes through when no custom backend auth extension is installed
  - deployment may protect backend routes through security groups, firewall rules, API Gateway, ingress policy, service mesh, or private listener placement
  - streamuploader does not decide whether external controls are sufficient for a deployment
authorization:
  permissions:
    - object:delete
    - object:rename
    - object:replace
    - prefix:purge
    - derived:invalidate
    - shared-key:create
    - shared-key:revoke
    - extracted-content:read
    - async-task:wait
  checks:
    - backend actor identity
    - tenant_id
    - object key namespace
    - requested backend permission
    - reason code for destructive operation when route requires it
constraints:
  - backend auth must not accept browser session cookie alone
  - backend routes must keep CORS disabled
  - upload capability token must never authorize backend control operation
  - custom middleware should reject missing or ambiguous backend actor identity when it performs application-level auth
auditing:
  - include backend_actor, subject, tenant_id, permission, object key, request id, decision, and reason
  - preserve policy:backend-control-plane-policy audit requirements
references:
  - api:auth-middleware-extension-api
  - data:auth-context
  - api:backend-control-api
  - policy:backend-control-plane-policy
  - policy:jwt-claim-authorization
  - policy:audit-log-policy
```
