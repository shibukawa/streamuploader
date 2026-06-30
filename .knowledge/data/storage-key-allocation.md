---
id: data:storage-key-allocation
type: data
title: Storage Key Allocation
---
Storage key allocation defines unguessable S3 prefixes and object keys returned to clients for capability-style use.

```yaml
fields:
  allocation_key: opaque id
  namespace:
    enum:
      - global
      - user_prefixed
      - custom_prefixed
  requested_prefix: client supplied prefix optional
  normalized_prefix: sanitized prefix optional
  random_token:
    alphabet: base62 alphanumeric only
    entropy_bits: configurable
    length_chars: derived from entropy_bits and alphabet
  storage_prefix: generated S3 prefix
  object_key: generated S3 object key
  display_key: folder plus file name returned to client
  original_name: client supplied file name optional
  safe_name: sanitized file name
examples:
  google_photos_like:
    namespace: global
    entropy_bits: 238
    token_length_base62: 40
  gist_like:
    namespace: user_prefixed
    entropy_bits: 190
    token_length_base62: 32
references:
  - policy:storage-key-allocation-policy
  - data:file-item
```

