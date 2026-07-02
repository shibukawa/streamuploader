---
id: flow:svg-preview-generation
type: flow
title: SVG Preview Generation
---
SVG preview generation rasterizes accepted SVG files into safe preview images with a sandboxed renderer.

```yaml
flow:
  trigger: data:file-item uploaded, allowed, and selected by policy:preview-generation-policy
  eligibility:
    - detected content type is SVG
    - SVG passes policy:file-intake-security checks
    - dimensions and expanded render size are within configured limits
  steps:
    - name: sanitize_svg
      actions:
        - reject script, event handler, foreignObject, and active content
        - reject or strip external references
        - cap entity expansion and input size
    - name: rasterize
      actions:
        - run sandboxed renderer
        - prefer system:svg-renderer
        - allow macOS sips fallback only after sanitize_svg has produced safe input or rejected unsafe input
        - render to fixed preview sizes
        - force transparent or configured background
    - name: encode_preview
      actions:
        - choose output format using policy:preview-format-policy
        - generate static preview asset
    - name: store_assets
      actions:
        - write generated preview to system:s3-storage
        - record data:derived-asset entry
  failure:
    optional_preview:
      - keep original file accepted when policy allows SVG original
      - mark preview failed or skipped
    required_preview:
      - block ready or commit until preview succeeds
references:
  - policy:preview-generation-policy
  - policy:preview-format-policy
  - data:derived-asset
  - system:svg-renderer
  - system:s3-storage
```
