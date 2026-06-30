---
id: policy:checksum-dedupe-policy
type: policy
title: Checksum Dedupe Policy
---
Checksum dedupe policy stores identical file content once while tracking logical references safely.

```yaml
policy:
  checksum:
    preferred: sha256
    computed: during upload stream
  canonical_store:
    key_by: checksum and optional tenant boundary
    object: physical blob
  logical_reference:
    per_file_item:
      - batch_key optional
      - upload_key
      - display_key
      - checksum
      - ref_target
  ref_count:
    - increment after upload acceptance succeeds
    - decrement on delete or expiration
    - delete physical blob only when count is zero and grace period elapsed
  race_safety:
    - conditional writes or lock object for ref count updates
    - idempotent delete
    - audit all ref count transitions
  derived_assets:
    - may be keyed by checksum plus transform parameters
references:
  - data:file-item
  - policy:audit-log-policy
```
