---
id: rule:prefix-replay
type: rule
title: Prefix Replay
---
Prefix inspection must not consume bytes irreversibly from the upload stream.

```yaml
rule:
  read:
    - bounded prefix from incoming stream
  replay:
    - prepend inspected bytes back into stream sent to S3
    - use io.MultiReader or equivalent Go primitive
  constraints:
    - prefix buffer has fixed maximum size
    - inspection errors fail closed in strict mode
references:
  - policy:file-intake-security
  - requirement:streaming-upload
```

