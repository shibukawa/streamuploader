---
id: decision:serverless-upload-state
type: decision
title: Serverless Upload State
---
Serverless upload nodes must not depend on local upload state.

```yaml
decision:
  selected:
    - stateless_service_nodes
    - durable_work_state_in_s3
    - service_side_sdk_multipart_upload
  state_locations:
    s3_work_sentinel:
      stores:
        - session status
        - storage key allocation
        - multipart upload id when resumable service-side multipart is used
        - part numbers and ETags when needed for completion
        - file security and progress summary
    s3_multipart_service:
      stores:
        - uploaded parts
        - upload id state recoverable by list parts
  transfer_policy:
    - do not use presigned URLs for S3 upload
    - handling node streams request body to S3 with AWS SDK multipart upload
    - persist multipart upload id and completed part ETags when resumability across nodes is needed
  concurrency:
    - update .work with optimistic ETag or equivalent conditional write where available
    - make progress updates idempotent
    - allow any node to resume from .work plus S3 multipart state
  rationale:
    - serverless instances can be replaced at any time
    - durable minimal state belongs in policy:work-sentinel-cleanup .work object
    - S3 is close to the service, so server-side multipart upload avoids hidden URL state
references:
  - policy:work-sentinel-cleanup
  - decision:upload-transport-boundary
  - system:s3-storage
```
