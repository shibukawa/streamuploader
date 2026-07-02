---
id: system:thumbnail-converter
type: system
title: Thumbnail Converter
---
Thumbnail converter creates static image thumbnails locally or through an explicitly configured external one-shot converter.

```yaml
roles:
  in_process_go:
    use_when:
      - source is decodable by Go image stack or registered codec
      - selected output backend is linked into current build
    pipeline:
      - tee upload stream to original object writer and thumbnail decoder when safe limits allow
      - resize in memory with pixel and byte limits
      - encode selected format
      - write data:derived-asset object to system:s3-storage
  local_tool:
    use_when:
      - Go cannot decode source
      - requested codec is unavailable in current build
      - deployment prefers native tool performance
    candidates:
      - sips on macOS only when startup probe detects the binary and requested output format support
      - vips for fast resize and AVIF/WebP output
      - ffmpeg for broad decode fallback only with encoders detected by startup probe
      - image/jpeg final fallback when modern output is unavailable
  external_webhook:
    use_when:
      - data:thumbnail-generation-config external_processing enabled
      - SU_THUMBNAIL_WEBHOOK_URL is configured
    behavior:
      - POST streamed source bytes to configured webhook URL
      - include desired size, fit, output policy, source object key, and lossless policy in request headers
      - accept 2xx response body as generated thumbnail bytes
      - read Content-Type plus optional X-Thumbnail-Width, X-Thumbnail-Height, and X-Thumbnail-Backend response headers
  tool_subcommand:
    name: thumbnail-convert
    image: system:linux-tool-worker-image or tools image
    purpose:
      - run one-shot conversion in Lambda, Cloud Run, worker container, or local command
      - keep front streamuploader image distroless while tools image carries codecs
    io_contract:
      input: source image bytes on stdin plus flags for width, height, fit, format, and lossless policy
      output: thumbnail bytes on stdout plus JSON conversion metadata on stderr
backend_selection:
  startup_plan:
    - build one thumbnail execution plan at process startup or thumbnail-convert startup
    - include CGO availability, Go encoder candidates, external webhook mode, sips formats, and ffmpeg encoders
    - runtime conversion follows the precomputed plan and does not perform encoder discovery
  go_decodable:
    cgo_enabled:
      - prefer vegidio/avif-go for image/avif encode when selected
      - prefer chai2010/webp for image/webp encode when selected
    cgo_disabled:
      - use external tools for AVIF/WebP when configured
      - use eringen/gowebper for pure-Go lossless/simple WebP fallback
      - use image/jpeg final static fallback
  go_not_decodable:
    - skip Go decode path
    - try configured external webhook first when enabled
    - try sips candidates from startup plan on macOS when applicable
    - try ffmpeg candidates from startup plan, preferring AVIF or WebP encoders before JPEG
    - future local tool expansion may add sips or vips before final fallback
    - fallback to image/jpeg only after successful decode and resize
performance:
  - use io.MultiWriter or equivalent tee so original upload storage and thumbnail decode can begin from one stream
  - isolate thumbnail errors from original upload unless execution mode requires generated asset readiness
  - sequential mode waits for converter completion before wait endpoint reports ready
  - async mode records pending data:derived-asset and lets converter finish independently
operations:
  - probe available libraries and tools at startup
  - log selected thumbnail backend and output format at info level
  - include backend version and fallback reason in audit/status when available
references:
  - data:thumbnail-generation-config
  - data:derived-asset
  - data:processor-result
  - system:s3-storage
  - system:linux-tool-worker-image
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
  - policy:external-delegation-policy
```
