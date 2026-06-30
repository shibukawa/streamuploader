---
id: policy:share-url-policy
type: policy
title: Share URL Policy
---
Share URL policy provides revocable capability-style aliases above S3 object keys.

```yaml
policy:
  alias_layer: API or CDN, not native S3 alias
  modes:
    short_lived_alias:
      - expires_at required
      - optional max download count
    revocable_alias:
      - revoked marker or share record in S3 metadata store
      - deny after revocation even if target exists
    rotated_alias:
      - create new share token
      - revoke old token
      - target object key may stay unchanged
  storage:
    - share records can live under control prefix in S3
    - include target key, access mode, expiry, revoked flag, and constraints
  audit:
    - create
    - resolve
    - deny
    - revoke
references:
  - api:share-url-api
  - policy:object-access-policy
```

