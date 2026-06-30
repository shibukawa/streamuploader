---
id: system:server-deployment-modes
type: system
title: Server Deployment Modes
---
Server deployment modes separate simple fronting reverse proxy use from normal protected integration use.

```yaml
modes:
  simple_fronting_reverse_proxy:
    description: streamuploader receives browser traffic and proxies non-upload paths to system:application-server
    routing:
      - upload endpoint defaults to /api/upload and is configurable
      - matching upload endpoint is handled by streamuploader
      - all other paths are reverse proxied to system:application-server
    cors:
      - not required because browser sees one origin
    headers:
      - preserve request id
      - strip hop-by-hop proxy headers
    auth:
      - simple mode does not login-protect streamuploader upload endpoints
      - application server login still protects application routes
  protected_forwarded_upload:
    description: application server or edge proxy owns public traffic and forwards upload API calls to streamuploader
    routing:
      - application or edge proxy applies login and authorization
      - only upload endpoint traffic is forwarded to streamuploader
      - preferred for production APIs that need protection
    cors:
      - usually not required when same-origin through application server
    headers:
      - streamuploader trusts configured forwarded identity and request headers only from trusted proxies
  standalone_cross_origin:
    description: streamuploader runs as a separate origin called directly by browser clients
    routing:
      - dedicated host or subdomain
    cors:
      - governed by policy:http-cors-header-policy
    note: use only when cross-origin upload API is acceptable
configuration:
  - mode
  - public_upload_port
  - backend_control_port
  - backend_control_listen_mode same_port or separate_port
  - upload_base_path default /api/upload
  - application_server_base_url
  - public_base_url
  - allowed_origins
  - allowed_methods
  - allowed_headers
  - exposed_headers
  - allow_credentials
  - max_age
  - trusted_proxy_headers
control_plane:
  - privileged operations use system:backend-control-listener
  - backend control routes are never exposed through browser-facing reverse proxy paths
health:
  - api:health-api /healthz is available on public listener
  - api:health-api /healthz is also available on backend control listener when separate port is configured
references:
  - policy:http-cors-header-policy
  - policy:reverse-proxy-routing-policy
  - system:backend-control-listener
  - system:application-server
  - api:upload-api
  - api:backend-control-api
  - api:health-api
  - api:session-progress-api
  - api:download-api
```
