---
id: system:clamav
type: system
title: ClamAV
---
ClamAV is an optional malware scanning integration for file intake.

```yaml
integration:
  enabled_by: SU_CLAMAV_HOST or CLAMAV_HOST environment variable
  optional: true
  backend_selection: policy:tool-backend-selection-policy
  transport:
    protocol: clamd TCP INSTREAM
    default_port: 3310
    request_shape:
      - open TCP connection to configured host
      - send INSTREAM command
      - send uploaded bytes as bounded chunks while intake stream is read
      - send zero-length chunk terminator
      - parse OK, FOUND, or ERROR response
  modes:
    stream_scan:
      required_when: SU_CLAMAV_HOST is configured
      behavior: reject before finalizing upload when detection is positive
    async_scan:
      preferred_when: large file scanning exceeds request budget
      behavior: upload into quarantine or pending state before release
constraints:
  - scanning must respect request timeout or background job limits
  - scan result must be captured in data:upload-record
  - service must not buffer full file content in memory or local disk for scan
  - scanner connection failure fails upload closed when system:clamav is enabled
  - system:clamav may apply archive scan limits but does not replace policy:archive-bomb-protection
streaming_behavior:
  intake:
    - upload body is copied through io.MultiWriter to system:s3-storage temporary key and clamd INSTREAM
    - temporary key uses sibling .tmp path until all security gates pass
    - FOUND response deletes .tmp object and returns malware_detected
    - OK response permits archive guard and final object publish
  publish:
    - copy .tmp object to final key after system:clamav and policy:archive-bomb-protection allow
    - delete .tmp object after publish, reject, or failed copy
references:
  - policy:file-intake-security
  - policy:archive-bomb-protection
  - requirement:streaming-upload
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
