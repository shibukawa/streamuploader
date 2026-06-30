---
id: data:upload-record
type: data
title: Upload Record
---
Upload record captures the local state needed to correlate stream upload, S3 object, and client metadata submission.

```yaml
fields:
  upload_id: service-generated idempotency key
  object_key: S3 object key after upload acceptance
  bucket: logical or physical S3 bucket
  content_type: trusted or detected media type
  size_bytes: counted during stream
  checksum: optional digest
  scan_status: optional result from policy:file-intake-security
  application_reference: optional id or metadata pointer returned later by system:application-server
  state:
    enum:
      - accepted
      - uploading
      - uploaded
      - ready_for_metadata
      - failed
      - cleanup_pending
references:
  - requirement:streaming-upload
  - requirement:application-metadata-submit
```
