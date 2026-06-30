---
id: policy:archive-bomb-protection
type: policy
title: Archive Bomb Protection
---
Archive bomb protection rejects compressed inputs that exceed configured expansion, nesting, entry, path, or time limits.

```yaml
rules:
  scope:
    - zip
    - tar
    - gzip
    - bzip2
    - xz
    - 7z when supported
    - office containers when inspection is enabled
  limits:
    max_uncompressed_bytes: configurable
    max_compression_ratio: configurable
    max_entries: configurable
    max_depth: configurable
    max_single_entry_bytes: configurable
    max_filename_bytes: configurable
    max_inspection_time_ms: configurable
  reject:
    - encrypted archives unless explicitly allowed
    - recursive nested archives beyond max_depth
    - zip slip paths with absolute path or parent traversal
    - duplicate confusing paths when policy disallows them
    - archive central directory claims that exceed limits
    - decompression requiring unsupported methods
  behavior:
    - inspect metadata without extracting to shared filesystem
    - stream-count decompression only inside bounded worker when needed
    - fail closed on parse ambiguity in strict mode
references:
  - policy:file-intake-security
  - data:security-check-result
```

