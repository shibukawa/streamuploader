---
id: system:application-server
type: system
title: Application Server
---
Application server owns normal product routes, login protection, and metadata persistence.

```yaml
integration:
  protocol: HTTP
  roles:
    - serve application frontend and APIs
    - validate and persist application metadata
    - own login, session, CSRF, and authorization policy
    - receive file facts returned by streamuploader from frontend
  reverse_proxy_mode:
    - streamuploader may forward non-upload paths to this server
    - same-origin browser use avoids CORS for simple deployments
    - this mode does not protect streamuploader upload endpoints
requirements:
  - stable metadata API contract owned by application
  - accept streamuploader returned object_key or display_key values
  - reject file facts not allowed for current user or product context when auth is available
references:
  - requirement:application-metadata-submit
  - policy:reverse-proxy-routing-policy
  - decision:consistency-model
```
