---
id: flow:hls-generation
type: flow
title: HLS Generation
---
HLS generation creates adaptive streaming derivatives from accepted video files.

```yaml
flow:
  trigger: video data:file-item accepted and selected by policy:preview-generation-policy or product policy
  steps:
    - name: probe_video
      actions:
        - ffprobe duration, resolution, codec, bitrate
    - name: transcode_ladder
      actions:
        - generate configured renditions
        - segment to HLS
        - strip audio when policy says preview only
    - name: store_playlist
      actions:
        - store master playlist and segments under derived prefix
        - record data:derived-asset entries
    - name: expose_stream
      actions:
        - status API exposes HLS playlist key
        - access policy controls playlist and segment delivery
constraints:
  - worker queue required
  - CPU and duration limits
  - optional DRM or signed segment access is future work
references:
  - system:media-converter
  - policy:worker-queue-policy
  - policy:object-access-policy
```

