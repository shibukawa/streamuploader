---
id: system:svg-renderer
type: system
title: SVG Renderer
---
SVG renderer rasterizes SVG previews in an isolated process, preferably using resvg or rsvg-convert with Inkscape as high fidelity fallback.

```yaml
components:
  preferred:
    - resvg in sandboxed worker when strict static SVG behavior is desired
    - rsvg-convert in sandboxed worker
  alternatives:
    - Inkscape CLI when higher SVG fidelity is required
    - sips on macOS for low-friction local fallback after SVG sanitization
    - qlmanage on macOS for generic thumbnail fallback
constraints:
  - run outside request path
  - disable external file and network access
  - reject active content before rendering
  - never pass unsanitized SVG to sips, qlmanage, or any generic image renderer
  - limit CPU, memory, wall time, input size, output dimensions, and pixel count
  - treat render failure as preview failure, not original file corruption
references:
  - flow:svg-preview-generation
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
