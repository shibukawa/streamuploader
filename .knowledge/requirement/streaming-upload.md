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
      - when post-stream security is enabled, write to sibling .tmp key before final publish
  security_streaming:
    - malware scan uses system:clamav TCP INSTREAM without full file buffering
    - scanner stream and S3 upload stream run from same request reader through io.MultiWriter
    - archive guard keeps policy:archive-bomb-protection as separate zip bomb control
    - final object key is published only after all enabled security gates pass
  constraints:
    - bounded memory
    - client must not bypass service file checks
    - cancel upstream work when client disconnects
    - propagate request context to S3 and upstream HTTP calls
    - delete .tmp object after reject, publish, or copy failure
  references:
    - flow:session-assembly
    - flow:security-gated-upload-acceptance
    - data:upload-record
    - decision:upload-transport-boundary
```
