---
id: system:media-converter
type: system
title: Media Converter
---
Media converter is an isolated worker using selected media backend such as ffmpeg or macOS avconvert.

```yaml
components:
  probe:
    preferred_tool: ffprobe
    purpose:
      - validate container and streams
      - detect duration, resolution, frame rate, and codec
  transcode:
    preferred_tool: ffmpeg
    fallback_tool: avconvert on macOS for simple preview when configured
    outputs:
      - animated_webp preferred
      - animated_avif optional
      - animated_gif compatibility
      - animated_png compatibility when acceptable
constraints:
  - run outside request path
  - isolate process and temporary files
  - disable network protocols in ffmpeg
  - limit CPU, memory, wall time, input duration, frame count, pixel count, and output size
  - strip audio and metadata from generated previews
  - treat conversion failure as preview failure, not original file corruption
references:
  - flow:video-preview-generation
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
