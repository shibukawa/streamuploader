---
id: flow:video-preview-generation
type: flow
title: Video Preview Generation
---
Video preview generation creates short animated preview assets from accepted video uploads with ffmpeg.

```yaml
flow:
  trigger: data:file-item uploaded, allowed, and selected by policy:preview-generation-policy
  eligibility:
    - detected content type is allowed video type
    - file item is clean or scan is not required before derivative work
    - duration, resolution, codec, and container are within configured limits
  steps:
    - name: probe_video
      actions:
        - run ffprobe in isolated worker
        - collect duration, dimensions, frame rate, codec, and stream metadata
        - reject unsupported or suspicious media
    - name: select_clip
      actions:
        - choose representative timestamp or short segment
        - avoid very first black frame when possible
        - cap frame count and output duration
    - name: generate_preview
      actions:
        - run ffmpeg with strict resource limits
        - scale to configured max dimensions
        - remove audio
        - output configured animated preview format
    - name: store_assets
      actions:
        - write generated preview to system:s3-storage
        - record data:derived-asset entry
  outputs:
    preferred:
      - animated_webp
      - animated_avif when supported
    compatible:
      - animated_gif
      - animated_png
  failure:
    optional_preview:
      - keep original file accepted
      - mark preview failed or skipped
    required_preview:
      - block ready or commit until preview succeeds
      - return actionable status
references:
  - policy:preview-generation-policy
  - data:derived-asset
  - system:media-converter
  - system:s3-storage
```

