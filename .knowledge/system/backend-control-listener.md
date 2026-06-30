---
id: system:backend-control-listener
type: system
title: Backend Control Listener
---
Backend control listener is a separate non-public HTTP listener for privileged backend and operator APIs.

```yaml
ports:
  public_upload_port:
    purpose:
      - browser upload key issuance when allowed
      - file content upload
      - upload status or watch API
    exposure: public or frontend-facing through proxy
  backend_control_port:
    purpose:
      - privileged object delete, rename, replace, purge, and derived invalidation
      - internal worker control and application-server integration
    exposure:
      - private network only
      - localhost, unix socket, service mesh, or private load balancer
    cors: disabled
    browser_access: forbidden
deployment_modes:
  same_port:
    description: backend control routes are mounted on the same listener when explicitly configured
    health:
      - api:health-api /healthz is served on the shared listener
  separate_port:
    description: backend control routes are served by a separate listener
    health:
      - api:health-api /healthz is served on both public and backend listeners
configuration:
  - public_listen_addr
  - backend_listen_addr
  - backend_listen_mode same_port or separate_port
  - backend_tls_mode
  - backend_mtls_ca
  - backend_allowed_cidrs
  - backend_auth_mode
  - backend_disable_public_routes
operations:
  - expose separate health/readiness for each listener
  - bind backend listener to private address by default
  - fail startup when backend listener is configured on public wildcard without explicit override
references:
  - api:backend-control-api
  - api:health-api
  - policy:backend-control-plane-policy
  - system:server-deployment-modes
```
