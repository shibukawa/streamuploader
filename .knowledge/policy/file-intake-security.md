---
id: policy:file-intake-security
type: policy
title: File Intake Security
---
File intake policy rejects risky uploads before durable storage whenever the decision can be made from bounded stream prefix inspection.

```yaml
rules:
  default_posture: whitelist
  prefix_inspection:
    read_limit_bytes: configurable
    checks:
      - system:content-detector lightweight type detection
      - magic number detection
      - known dangerous executable signatures
      - shebang and script language detection
      - archive or polyglot indicators when relevant
      - declared versus detected content type mismatch
  allowlist:
    managed_by: configuration
    values:
      - media type
      - extension only as secondary hint
      - magic signature
  optional_scan:
    engine: system:clamav
    mode:
      - stream scan when available
      - asynchronous scan only when product accepts quarantine workflow
  reject:
    - executable formats unless explicitly allowed
    - shell script or other script uploaded under non-script media type
    - unknown type when whitelist mode is strict
    - file exceeding configured size limit
    - archive violating policy:archive-bomb-protection
  references:
    - rule:prefix-replay
    - requirement:streaming-upload
    - system:content-detector
    - policy:archive-bomb-protection
    - data:security-check-result
```
