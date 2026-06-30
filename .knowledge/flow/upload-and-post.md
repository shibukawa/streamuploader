---
id: flow:upload-and-post
type: flow
title: Upload And Post
---
Upload flow streams file bytes to S3 and returns storage facts; application metadata submit is performed by the frontend against the application server.

```yaml
flow:
  trigger: client obtains upload_key and sends file bytes
  steps:
    - name: accept_upload
      actions:
        - validate upload_key
        - open file stream with request size limits
        - start request-scoped context
    - name: inspect_prefix
      actions:
        - read bounded prefix
        - run policy:file-intake-security
        - replay prefix into S3 stream
    - name: run_parallel_work
      parallel:
        file_upload:
          - multipart upload to system:s3-storage
          - compute checksum and byte count during streaming
          - return object key after complete multipart upload
        progress:
          - publish api:session-progress-api events
          - update data:file-item state
    - name: finalize_upload_key
      actions:
        - persist durable object key, size, checksum, and content type
        - mark upload_key uploaded or failed
    - name: respond
      actions:
        - return data:file-item facts
        - client may later call api:upload-api wait_uploads
  failure:
    upload_fails:
      - abort multipart upload
      - mark upload_key failed
      - do not affect application metadata because streamuploader does not post it
```
