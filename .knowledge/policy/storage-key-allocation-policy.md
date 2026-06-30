---
id: policy:storage-key-allocation-policy
type: policy
title: Storage Key Allocation Policy
---
Storage key allocation policy creates configurable folder names and object keys using cryptographic randomness and optional client prefixes.

```yaml
policy:
  token_generation:
    source: cryptographic random bytes
    encoding: base62 alphanumeric without symbols
    entropy_bits: configured per namespace
    length_rule: ceil(entropy_bits / log2(62))
  namespace_modes:
    global:
      layout: "{random_token}/{safe_file_name}"
      use_case: Google Photos style shared keyspace
      example_entropy_bits: 238
      example_token_chars: 40
    user_prefixed:
      layout: "{user_prefix}/{random_token}/{safe_file_name}"
      use_case: gist style user path plus random token
      example_entropy_bits: 190
      example_token_chars: 32
    custom_prefixed:
      layout: "{normalized_prefix}/{random_token}/{safe_file_name}"
      use_case: sender selected grouping prefix
  prefix_rules:
    - client may request prefix when policy allows it
    - prefix is normalized to safe path segments
    - reject absolute paths, parent traversal, empty hidden control names, and reserved marker names
    - cap prefix length and segment count
    - do not trust prefix for authorization
  file_name_rules:
    - keep original_name as metadata only
    - sanitize safe_file_name for object key display
    - service may append collision-resistant suffix when needed
  capability_warning:
    - unguessable key can be used as capability-style URL only when storage or CDN access policy allows it
    - entropy does not replace auth for private data unless explicitly accepted by product policy
references:
  - data:storage-key-allocation
  - api:upload-api
  - decision:upload-transport-boundary
```

