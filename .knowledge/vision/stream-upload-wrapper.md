---
id: vision:stream-upload-wrapper
type: vision
title: Stream Upload Wrapper
---
Go web service for service-mediated file uploads, upload progress watching, and optional same-origin reverse proxying.

```yaml
goals:
  primary:
    - issue upload keys to clients
    - accept file bytes through streamuploader endpoints only
    - upload file bodies to S3-compatible storage without full buffering
    - wait for one or more upload keys and return durable file facts
    - let client submit application metadata to system:application-server with returned file facts
    - keep upload path efficient for large files
    - optionally reverse proxy non-upload paths to system:application-server for simple same-origin use
    - support configurable native and local command processors for extraction, previews, OCR, HLS, and custom enrichment
  non_goals:
    - validate application metadata
    - submit metadata to application server
    - provide login protection for streamuploader endpoints in simple reverse proxy mode
    - natively model every external API shape
  related:
    - api:upload-api
    - requirement:streaming-upload
    - requirement:application-metadata-submit
    - flow:session-assembly
    - policy:processor-execution-policy
    - system:local-command-processor
    - policy:reverse-proxy-routing-policy
    - policy:file-intake-security
```
