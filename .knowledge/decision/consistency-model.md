---
id: decision:consistency-model
type: decision
title: Consistency Model
---
The first policy should prefer durable S3 upload before the frontend submits application metadata.

```yaml
decision:
  selected: upload_then_client_metadata_submit
  rationale:
    - application metadata should include final S3 object keys for selected files
    - stream upload cannot know success until each multipart upload completes
    - waiting before app submit avoids records pointing to missing or rejected objects
    - streamuploader avoids application-specific consistency and retry policy
  tradeoffs:
    client_never_submits_metadata:
      impact: uploaded object may be orphaned from the application perspective
      mitigation:
        - cleanup job for expired upload keys
        - object tags marking pending state
    upload_failure:
      impact: frontend should not submit metadata with failed file facts
      mitigation:
        - abort multipart upload
        - wait endpoint reports failed upload keys
  alternatives:
    streamuploader_posts_metadata:
      problem: duplicates app schema, auth, validation, retry, and idempotency concerns
    client_direct_presigned_upload:
      problem: changes wrapper responsibility and needs separate finalization API
references:
  - requirement:application-metadata-submit
  - flow:session-assembly
```
