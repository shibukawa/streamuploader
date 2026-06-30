---
id: policy:download-variant-policy
type: policy
title: Download Variant Policy
---
Download variant policy creates or promotes delivery-optimized compressed objects according to storage and compatibility goals.

```yaml
policy:
  default: keep_original_and_generate_variant
  storage_modes:
    keep_original_and_generate_variant:
      behavior:
        - original object remains canonical
        - compressed variants are derived assets linked to data:file-item
      tradeoff: higher storage, broad compatibility
    replace_original_same_key:
      behavior:
        - rewrite original object key with compressed bytes
        - set Content-Encoding to zstd on that object
        - keep Content-Type as original media type
      tradeoff: lower storage, requires clients or serving layer to support encoding
    promote_variant_delete_original:
      behavior:
        - write compressed variant under separate key
        - update metadata payload to promoted compressed key
        - delete original after successful promotion
      tradeoff: lower storage, capability URL changes unless display key is remapped
  compression:
    zstd:
      generate_when:
        - source content type is compressible
        - source size exceeds threshold
        - expected download frequency justifies worker cost
      content_encoding: zstd
      object_metadata:
        Content-Type: original content type
        Content-Encoding: zstd
        Vary: Accept-Encoding when served through HTTP layer
      avoid_for:
        - images such as jpeg, png, avif, webp, heic
        - video
        - audio
        - archives such as zip, gzip, zstd, xz, 7z
        - Office Open XML and OpenDocument files when already zip containers
        - PDFs when sampling shows poor compression benefit
        - encrypted or random-like content
        - very small files
    gzip:
      generate_when:
        - legacy client support is required
  serving:
    - choose variant based on Accept-Encoding and client support
    - fall back to original when zstd is unsupported
    - do not set Content-Encoding unless object bytes are actually encoded
    - replacement modes require product acceptance that raw capability URLs may require encoding support
  cleanup:
    - delete variants with parent session prefix when uncommitted
    - expire unused variants by lifecycle policy when configured
references:
  - data:derived-asset
  - flow:download-variant-generation
  - system:s3-storage
```
