---
id: api:share-url-api
type: api
title: Share URL API
---
Share URL API is the general share-link concept; the first concrete implementation is api:shared-key-api.

```yaml
endpoints:
  create_share:
    method: POST
    path: /shares
    body:
      target_key: display_key or object_key
      expires_at: optional timestamp
      access_mode: policy:object-access-policy mode
      constraints: optional audience, max downloads, jwt claim rules
    response:
      share_id: opaque token
      share_url: API URL, not S3 alias
  resolve_share:
    method: GET
    path: /s/{share_id}
    behavior:
      - validate expiry and revocation
      - apply optional policy:jwt-claim-authorization
      - redirect to S3, issue presigned URL, or proxy bytes
  revoke_share:
    method: DELETE
    path: /shares/{share_id}
    behavior:
      - mark share revoked
      - audit event
notes:
  - S3 has object keys, not general symlink or alias semantics
  - API or CDN layer owns alias, revocation, and key rotation behavior
  - api:shared-key-api stores minimal alias records in S3 and serves bytes through api:download-api
references:
  - policy:object-access-policy
  - policy:share-url-policy
  - api:shared-key-api
  - policy:audit-log-policy
```
