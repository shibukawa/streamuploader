---
id: data:auth-context
type: data
title: Auth Context
---
Auth context carries caller identity and authorization facts produced by customizable auth middleware.

```yaml
purpose:
  - give downstream handlers stable request-scoped auth facts
  - avoid coupling core upload logic to one identity provider
  - allow frontend and backend auth implementations to differ
fields:
  subject:
    type: string
    meaning: user id, service account id, or anonymous capability holder
  tenant_id:
    type: string optional
  roles:
    type: list string optional
  scopes:
    type: list string optional
  permissions:
    type: list string optional
  object_prefixes:
    type: list string optional
    meaning: storage key prefixes caller may access
  upload_owner:
    type: string optional
    meaning: owner token or owner id for upload key ownership checks
  backend_actor:
    type: string optional
    meaning: service account or operator identity for backend audit
  raw_claims:
    type: map optional
    constraint: do not log secrets or full bearer tokens
request_binding:
  - middleware stores data:auth-context in request context
  - handlers read typed helper, not provider-specific headers
  - absent context means anonymous unless route policy requires auth
references:
  - policy:frontend-auth-extension-policy
  - policy:backend-auth-extension-policy
  - policy:jwt-claim-authorization
  - policy:audit-log-policy
```
