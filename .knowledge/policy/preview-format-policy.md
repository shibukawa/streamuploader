---
id: policy:preview-format-policy
type: policy
title: Preview Format Policy
---
Preview format policy selects efficient generated preview encodings by source content and visual characteristics.

```yaml
policy:
  static_image_outputs:
    lossy:
      preferred: image/avif
      fallback:
        - image/webp
        - image/jpeg
      use_for:
        - photo-like images
        - thumbnails where exact pixels are not required
    lossless:
      preferred: image/webp
      fallback:
        - image/png
      use_for:
        - flat graphics
        - screenshots
        - diagrams
        - SVG raster previews requiring sharp edges
  animated_outputs:
    preferred:
      - image/webp
      - image/avif when target clients support animated AVIF
    fallback:
      - image/gif
      - image/apng
  controls:
    - preserve alpha only when needed
    - avoid upscaling
    - cap dimensions and output bytes
    - allow per-tenant or per-role override
references:
  - flow:image-thumbnail-generation
  - flow:svg-preview-generation
  - flow:video-preview-generation
```

