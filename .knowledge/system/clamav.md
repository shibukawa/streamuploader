---
id: system:clamav
type: system
title: ClamAV
---
ClamAV is an optional malware scanning integration for file intake.

```yaml
integration:
  enabled_by: deployment configuration
  backend_selection: policy:tool-backend-selection-policy
  modes:
    stream_scan:
      preferred_when: scanner can consume stream during intake
      behavior: reject before finalizing upload when detection is positive
    async_scan:
      preferred_when: large file scanning exceeds request budget
      behavior: upload into quarantine or pending state before release
constraints:
  - scanning must respect request timeout or background job limits
  - scan result must be captured in data:upload-record
references:
  - policy:file-intake-security
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
```
