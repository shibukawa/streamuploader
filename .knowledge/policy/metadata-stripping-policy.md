---
id: policy:metadata-stripping-policy
type: policy
title: Metadata Stripping Policy
---
Metadata stripping policy removes privacy-sensitive embedded metadata from generated derivatives and optional originals.

```yaml
targets:
  image:
    strip:
      - EXIF GPS
      - camera serial
      - author
      - software
  pdf:
    strip:
      - author
      - creator
      - producer
      - embedded metadata when safe
  office:
    strip:
      - author
      - revision history
      - comments when policy requires
  media:
    strip:
      - metadata tags
      - GPS
behavior:
  - derived assets strip by default
  - original stripping is configurable and may create sanitized replacement
  - preserve orientation only when needed for correct rendering
references:
  - data:derived-asset
  - policy:preview-generation-policy
```

