---
id: policy:http-cors-header-policy
type: policy
title: HTTP CORS Header Policy
---
HTTP CORS header policy controls browser access only when streamuploader is exposed as a standalone cross-origin upload server.

```yaml
cors:
  config:
    allowed_origins:
      source: environment variable
      env: ALLOWED_ORIGINS
      default: "*"
      meaning: shared allowlist for CORS response and websocket Origin validation
      format: comma separated exact origins or wildcard for local/dev only
      startup_warning:
        - log warning when allowed_origins contains wildcard
        - warning explains wildcard is convenient for local/demo use and should be replaced by explicit origins for public deployments
      production:
        - prefer explicit origins
        - reject wildcard when credentials are enabled
        - reject wildcard when deployment policy marks standalone public production
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
    - validate Origin during upgrade using same allowed_origins config as CORS
    - reject cross-origin upgrade when Origin is absent and deployment requires browser origin proof
    - preserve upgrade headers through trusted proxies
  download:
    - support Range and If-Range where policy allows
    - return Content-Type, Content-Length, ETag, Last-Modified, Accept-Ranges, Content-Range when available
    - apply cache headers from data:http-cache-config
  placeholder:
    - Cache-Control: no-store, no-cache, max-age=0
security_headers:
  required:
    - X-Content-Type-Options: nosniff
  optional:
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
  - data:http-cache-config
```
