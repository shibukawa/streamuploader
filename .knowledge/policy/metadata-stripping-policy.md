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
      - capture timestamp
      - camera serial
      - camera or device information
      - author
      - software
      - comments
      - identifying EXIF, XMP, or IPTC tags
    preserve:
      - Orientation
      - ICC Profile
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
      - capture timestamp
      - device identifiers
behavior:
  - derived assets strip by default
  - EXIF-capable uploaded images and videos sanitize metadata by default through policy:file-type-sanitization-policy
  - original stripping is configurable and may create sanitized replacement before final storage commit
  - preserve Orientation and ICC Profile only when needed for correct rendering
  - do not re-encode media for metadata sanitize
references:
  - data:derived-asset
  - policy:preview-generation-policy
  - policy:file-type-sanitization-policy
```
