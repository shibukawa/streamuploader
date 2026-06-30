---
id: requirement:application-metadata-submit
type: requirement
title: Application Metadata Submit
---
Frontend submits application metadata to the application server after streamuploader confirms selected upload keys are durable.

```yaml
requirements:
  application_server: system:application-server
  payload:
    include:
      - client metadata JSON
      - list of files with streamuploader returned display_key and S3 object_key
      - per-file content type when trusted
      - per-file size when known or counted
      - per-file checksum when available
  behavior:
    - client creates upload keys before or during form editing
    - client uploads files through streamuploader endpoints
    - client watches selected upload keys through WebSocket when incremental UI updates are needed
    - client may use blocking wait endpoint for simple final submit flows
    - client posts application metadata and file facts to system:application-server
    - streamuploader does not validate or forward application metadata
    - system:application-server owns login, authorization, CSRF, schema validation, and persistence
  references:
    - flow:session-assembly
    - data:upload-batch
    - data:metadata-payload
    - decision:consistency-model
```
