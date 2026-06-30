---
id: policy:reverse-proxy-routing-policy
type: policy
title: Reverse Proxy Routing Policy
---
Reverse proxy routing policy lets streamuploader sit in front of an application server for simple same-origin deployments.

```yaml
policy:
  upload_endpoint:
    default_base_path: /api/upload
    configurable: true
    routing:
      - requests under upload_endpoint are handled by streamuploader
      - all other paths are reverse proxied to system:application-server
  purpose:
    - avoid CORS configuration for simple deployments
    - let frontend call upload API and application API from one origin
    - keep application routes usable behind a single local or edge entrypoint
  limitations:
    - simple mode only
    - streamuploader endpoints are not login-protected by this mode
    - application server login protects application routes only
    - production auth should normally be enforced by an upstream service or edge proxy before forwarding to streamuploader
  normal_production_pattern:
    - system:application-server or edge proxy receives browser traffic
    - protected upload API paths are forwarded to streamuploader after application auth
    - streamuploader trusts only configured forwarded headers and caller context
  proxy_behavior:
    - preserve method, path, query, and body for non-upload requests
    - forward request id and safe forwarding headers
    - strip hop-by-hop headers
    - support streaming request and response bodies
references:
  - system:server-deployment-modes
  - system:application-server
  - api:upload-api
  - policy:http-cors-header-policy
```
