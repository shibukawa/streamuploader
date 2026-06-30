---
id: api:health-api
type: api
title: Health API
---
Health API exposes liveness and readiness checks on public and backend listeners.

```yaml
endpoints:
  liveness:
    method: GET
    path: /healthz
    response:
      status: ok
  readiness:
    method: GET
    path: /readyz
    optional: true
    response:
      status: ok or degraded
listener_modes:
  same_port:
    - public upload routes, backend control routes, and health routes share one listener
    - /healthz is available on the shared listener
  separate_ports:
    - public upload listener exposes /healthz for frontend-facing process health
    - backend control listener exposes /healthz for backend/control-plane health
    - probes can target either or both listeners
constraints:
  - health responses do not expose secrets
  - backend-only readiness detail is returned only on backend listener when enabled
  - health route must not require browser CORS
references:
  - system:backend-control-listener
  - system:server-deployment-modes
```
