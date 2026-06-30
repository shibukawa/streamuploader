---
id: system:s3-storage
type: system
title: S3 Storage
---
S3-compatible object storage receives the uploaded file body through efficient streaming mechanisms.

```yaml
integration:
  methods:
    - multipart upload from Go service
  required_capabilities:
    - abort incomplete multipart upload
    - list multipart upload parts when resuming or completing
    - object key generation controlled by service
    - list and delete objects by session prefix for policy:work-sentinel-cleanup
    - delete canonical object by backend control request
    - copy object then delete source for rename semantics
    - replace object body and metadata by backend control request
    - small sentinel object writes such as .work
    - optional checksum metadata
    - optional object tags for pending or scan state
references:
  - requirement:streaming-upload
  - flow:session-assembly
  - decision:upload-transport-boundary
  - policy:work-sentinel-cleanup
  - api:backend-control-api
  - policy:backend-control-plane-policy
  - decision:serverless-upload-state
```
