---
id: system:svg-renderer
type: system
title: SVG Renderer
---
SVG renderer rasterizes SVG previews in an isolated process, preferably using rsvg-convert with Inkscape as high fidelity fallback.

```yaml
components:
  preferred:
    - rsvg-convert in sandboxed worker
  alternatives:
    - resvg when stricter SVG support is desired
    - Inkscape CLI when higher SVG fidelity is required
    - qlmanage on macOS for generic thumbnail fallback
constraints:
  - run outside request path
  - disable external file and network access
  - reject active content before rendering
  - limit CPU, memory, wall time, input size, output dimensions, and pixel count
  - treat render failure as preview failure, not original file corruption
references:
  - flow:svg-preview-generation
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
