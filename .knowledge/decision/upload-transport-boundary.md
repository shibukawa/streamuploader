---
id: decision:upload-transport-boundary
type: decision
title: Upload Transport Boundary
---
Clients must upload file bytes through this service so intake checks always run before durable S3 acceptance.

```yaml
decision:
  selected: service_mediated_upload
  client_path:
    - client sends bytes to api:upload-api content endpoint
    - service inspects prefix and enforces policy:file-intake-security
    - service streams accepted bytes to system:s3-storage
  s3_transfer:
    allowed_implementation:
      - AWS SDK multipart upload from service
    not_allowed:
      - client-visible S3 presigned upload URL
      - client direct-to-S3 upload bypassing file checks
      - service-owned presigned S3 part URLs
      - HTTP streaming proxy to presigned S3 URLs
  rationale:
    - otherwise clients can use S3 presigned URLs directly without this service
    - wrapper value is security checks, upload state, and durable file facts
    - service must observe bytes to compute size, checksum, and file verdict
    - S3 is near this service, so SDK multipart upload is simpler than hiding presigned URLs
references:
  - api:upload-api
  - requirement:streaming-upload
  - policy:file-intake-security
  - decision:serverless-upload-state
```
