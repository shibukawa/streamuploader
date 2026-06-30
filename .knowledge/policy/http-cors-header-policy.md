---
id: policy:http-cors-header-policy
type: policy
title: HTTP CORS Header Policy
---
HTTP CORS header policy controls browser access only when streamuploader is exposed as a standalone cross-origin upload server.

```yaml
cors:
  backend_control:
    - disabled for api:backend-control-api
    - browser origins are never allowlisted for backend control listener
  same_origin_modes:
    - no CORS required for system:server-deployment-modes simple_fronting_reverse_proxy
    - no CORS required when system:application-server or edge proxy forwards same-origin upload paths
  default:
    - deny cross-origin unless explicitly configured
    - never use wildcard origin when credentials are allowed
    - reflect only origins that match allowlist
  preflight:
    method: OPTIONS
    allow_methods:
      - GET
      - POST
      - PUT
      - DELETE
      - OPTIONS
    allow_headers:
      - Authorization
      - Content-Type
      - Idempotency-Key
      - X-Request-ID
      - X-Upload-Session-Key
      - X-Upload-File-Key
    max_age: configurable
  expose_headers:
    - Location
    - ETag
    - Content-Range
    - Accept-Ranges
    - Retry-After
    - X-Request-ID
    - X-Upload-Session-Key
    - X-Upload-File-Key
  credentials:
    - allow only when JWT/cookie mode requires it
    - require explicit origin allowlist
protocol_headers:
  websocket:
    - validate Origin during upgrade
    - preserve upgrade headers through trusted proxies
  download:
    - support Range and If-Range where policy allows
    - return Content-Type, Content-Length, ETag, Accept-Ranges, Content-Range
  placeholder:
    - Cache-Control: no-store, no-cache, max-age=0
security_headers:
  - X-Content-Type-Options: nosniff
  - Referrer-Policy configurable
  - Content-Security-Policy for API-generated HTML/SVG responses if any
  - HSTS owned by edge or standalone server, not both
proxy:
  - trust X-Forwarded-* only from configured proxies
  - preserve request id or generate one
references:
  - system:server-deployment-modes
  - system:backend-control-listener
  - api:backend-control-api
  - policy:reverse-proxy-routing-policy
  - api:session-progress-api
  - api:download-api
  - policy:placeholder-serving-policy
```
