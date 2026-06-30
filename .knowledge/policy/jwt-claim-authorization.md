---
id: policy:jwt-claim-authorization
type: policy
title: JWT Claim Authorization
---
JWT claim authorization validates bearer tokens and evaluates configured claim rules before object access or session operations.

```yaml
policy:
  token_validation:
    - verify signature using configured issuer keys or JWKS
    - validate iss, aud, exp, nbf, iat
    - reject missing or malformed bearer token when auth is required
  claim_rules:
    examples:
      owner_match:
        - claim sub equals object owner id
      tenant_match:
        - claim tenant_id equals session tenant id
      role_or_scope:
        - scope contains file:read
        - roles contains uploader_admin
      custom_claim:
        - claim value matches configured metadata field
  decisions:
    - allow
    - deny
    - issue_presigned_download
    - proxy_download
  audit:
    - subject
    - tenant
    - object_key or display_key
    - decision
    - reason
references:
  - policy:object-access-policy
```

