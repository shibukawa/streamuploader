---
id: requirement:streaming-upload
type: requirement
title: Streaming Upload
---
The service must process uploaded files as streams and avoid holding whole objects in memory or local disk.

```yaml
requirements:
  runtime: Go
  input:
    - upload session key
    - metadata JSON request
    - one or more file content streams
  storage:
    target: system:s3-storage
    strategy:
      - multipart upload from service to S3
  constraints:
    - bounded memory
    - client must not bypass service file checks
    - cancel upstream work when client disconnects
    - propagate request context to S3 and upstream HTTP calls
  references:
    - flow:session-assembly
    - data:upload-record
    - decision:upload-transport-boundary
```
