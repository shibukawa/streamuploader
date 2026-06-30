---
id: policy:shared-key-policy
type: policy
title: Shared Key Policy
---
Shared key policy defines capability-token creation, storage, and resolution for proxy downloads.

```yaml
configuration:
  enable_shared_key:
    type: bool
    default: false
    meaning: backend API can create and resolve data:shared-key-record
  shared_key_bits:
    type: int
    default: 128
    minimum: 96
    meaning: random entropy before URL-safe encoding
  shared_key_prefix:
    type: string
    default: .streamuploader/shared/
    meaning: S3 control prefix for data:shared-key-record
  allow_frontend_file_access:
    type: bool
    default: false
    meaning: frontend API may stream object bytes
security:
  - shared_key is bearer credential
  - generate with cryptographic randomness
  - reject creation when enable_shared_key is false
  - reject download when allow_frontend_file_access is false
  - reject expired or revoked records
  - do not include target_object_key in shared URL path
  - avoid logging full shared_key in application logs
revocation:
  preferred: delete global data:shared-key-record and per-object marker
  object_delete: delete all matching per-object markers and their global records
  note: one target object may have multiple shared keys
audit_events:
  - shared_key_create
  - shared_key_resolve
  - shared_key_deny
  - shared_key_revoke
references:
  - data:shared-key-record
  - api:shared-key-api
  - api:download-api
  - policy:object-access-policy
  - policy:audit-log-policy
```
