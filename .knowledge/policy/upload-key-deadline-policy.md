---
id: policy:upload-key-deadline-policy
type: policy
title: Upload Key Deadline Policy
---
Upload key deadline policy limits key replay and unfinished upload cost using S3-backed deadline marker objects.

```yaml
policy:
  goal:
    - reject stale upload keys before body transfer starts
    - stop uploads that exceed allowed wall-clock duration
    - keep cleanup possible without a local database
  marker:
    prefix: .uploading/
    key: .uploading/{upload_key}
    content_type: application/json
    created_at: upload key creation time
    deleted_at: after successful upload commit or terminal failure cleanup
    metadata:
      upload_key: opaque key
      object_key: target canonical object key
      temp_object_key: temporary staged key optional
      upload_start_deadline: timestamp
      upload_finish_deadline: timestamp
      status: key_created or uploading or uploaded or failed
  create_upload_key:
    - create marker when api:upload-api create_upload_key succeeds
    - upload_start_deadline defaults to created_at plus 10 seconds
    - upload_finish_deadline defaults to created_at plus 1 minute
    - response expires_at equals or does not exceed upload_start_deadline for client-visible start validity
  upload_start_check:
    - read marker before accepting api:upload-api upload_file body
    - reject missing marker as expired_or_unknown_upload_key
    - reject current time after upload_start_deadline before reading large body
    - atomically or conditionally transition marker status to uploading when storage backend supports it
  upload_finish_deadline:
    - derive request context with deadline upload_finish_deadline
    - pass context through storage upload, ClamAV scan, archive inspection, and staged object copy
    - abort upload and return timeout error when context deadline is exceeded
    - delete staged temporary object on timeout where possible
  completion:
    - delete marker after successful canonical object commit
    - terminal failure may delete marker after failure facts are recorded
  cleanup:
    - list .uploading/ marker objects
    - delete expired markers and associated temporary objects
    - optionally delete canonical object only when marker proves upload never completed
    - idempotent and safe to run repeatedly
references:
  - api:upload-api
  - policy:work-sentinel-cleanup
  - data:upload-deadline-config
  - system:s3-storage
  - flow:security-gated-upload-acceptance
```
