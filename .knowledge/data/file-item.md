---
id: data:file-item
type: data
title: File Item
---
File item represents one upload key and its eventual S3 object.

```yaml
fields:
  upload_key: opaque unique key
  batch_key: optional data:upload-batch wait batch key
  role: optional semantic role
  original_name: optional client file name
  object_key: S3 object key after storage commit
  storage_allocation: data:storage-key-allocation
  display_key: folder plus file name returned to client
  content_type: detected or trusted media type
  declared_content_type: client supplied media type optional
  size_bytes: counted or declared size
  uploaded_bytes: counted during stream
  checksum: optional digest
  derived_assets:
    type: list of data:derived-asset
  status:
    enum:
      - key_created
      - uploading
      - uploaded
      - rejected
      - scan_pending
      - clean
      - failed
  security_decision: allow or reject
  security_check: data:security-check-result
  client_temp_id: optional UI correlation id
references:
  - data:upload-batch
  - api:session-progress-api
  - data:storage-key-allocation
  - policy:file-intake-security
  - data:derived-asset
  - data:security-check-result
```
