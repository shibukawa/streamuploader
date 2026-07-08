---
id: api:auth-middleware-extension-api
type: api
title: Auth Middleware Extension API
---
Auth middleware extension API lets deployments replace frontend and backend request authentication without modifying streamuploader core.

```yaml
go_surface:
  package: streamuploader/auth
  frontend:
    function: NewFrontendAuthMiddleware
    signature: func(next http.Handler, config *Config) http.Handler
    setter: SetFrontendAuthMiddleware
    wraps:
      - browser-facing upload routes
      - browser-facing file and shared-key download routes
      - reverse-proxy frontend routes when mounted by streamuploader
  backend:
    function: NewBackendAuthMiddleware
    signature: func(next http.Handler, config *Config) http.Handler
    setter: SetBackendAuthMiddleware
    wraps:
      - api:backend-control-api routes
      - privileged internal routes mounted on backend listener
contract:
  - middleware may authenticate request before calling next
  - middleware may reject with JSON HTTP error before calling next
  - middleware may attach data:auth-context to request context
  - middleware must preserve request body and path values for next handler
  - middleware must be safe for concurrent requests
  - middleware must not perform storage mutations
  - middleware must not log bearer tokens, cookies, private keys, or full raw claims
default_behavior:
  frontend:
    - pass through when no custom frontend auth is configured
    - existing upload capability, owner cookie, shared key, and object access policies still apply
  backend:
    - no built-in BackendAuthToken bearer check
    - no built-in mTLS or service account JWT verifier
    - pass through when no custom backend auth is configured
    - deployment may rely on external network or gateway controls outside streamuploader
failure_response:
  unauthenticated:
    status: 401
    error: unauthorized
  unauthorized:
    status: 403
    error: forbidden
integration_modes:
  - linked package init calls streamuploader/auth setter at startup
  - custom main calls streamuploader/auth setters before streamuploadercli.Main
  - downstream fork or distribution imports deployment auth package
removed_core_mechanisms:
  - BackendAuthToken bearer token authorization
  - hard-coded mTLS preference
  - hard-coded service account JWT preference
  - hard-coded network boundary judgment
config:
  - auth extension reads only non-secret policy from Config
  - secrets come from environment, mounted files, or deployment secret manager
sample_policy:
  - project provides no auth adapter package
  - README may show inline custom main examples
  - deployment-owned package may call setters before streamuploadercli.Main
references:
  - policy:frontend-auth-extension-policy
  - policy:backend-auth-extension-policy
  - data:auth-context
  - policy:auth-extension-license-exception
  - api:upload-api
  - api:download-api
  - api:backend-control-api
```
