---
id: requirement:mime-magic-consistency
type: requirement
title: MIME Magic Consistency
---
Uploads must verify declared MIME type against magic-header detection before writing object bytes to durable storage.

```yaml
requirements:
  scope:
    first_phase: declared MIME versus detected MIME from bounded prefix
    later_phase:
      - gzip bomb and archive expansion policy:archive-bomb-protection
      - allowlist and denylist filtering from configuration
      - deeper malware scan through system:clamav when enabled
  input:
    declared_content_type:
      - data:file-item content_type from create key request
      - upload request Content-Type header fallback
    body: streaming HTTP request body
  prefix_read:
    limit_bytes: data:security-policy-config mime_magic.prefix_bytes default 3072
    memory: bounded byte slice only, never full file
    eof_before_limit: valid small upload, inspect available bytes
  configuration:
    env:
      path: SU_SECURITY_CONFIG
      legacy_fallback: SECURITY_CONFIG accepted for compatibility only
    file_format: YAML
    startup: load once before serving requests
    validation:
      - validate YAML against built-in JSON Schema before unmarshalling
      - reject unknown allow_file_types and deny_file_types keys
      - reject unknown allow_mime_types and deny_mime_types keys
      - reject unknown allowed_script_types and allowed_script_extensions keys
      - reject legacy list syntax for type switches
    defaults:
      mime_magic.enabled: true
      mime_magic.prefix_bytes: 3072
      mime_magic.reject_script_uploads: true
    lists:
      allow_file_types: bool switch map for aliases such as images, png, jpeg, pdf
      allow_mime_types: optional accept list for large future policy sets
      deny_file_types: bool switch map for aliases such as exe or archives
      deny_mime_types: optional reject list checked before allow
      allowed_script_types: opt-in bool switch map for shell, python, node, ruby, perl, php
      allowed_script_extensions: opt-in bool switch map for sh, py, js, rb, pl, php
      equivalent_mime_types: alias groups for MIME comparison
  detection:
    primary: decision:mime-detector-library
    fallback: Go net/http DetectContentType for unknown primary result
    compare: normalized MIME essence, without parameters
  accept:
    - declared MIME missing and detected MIME allowed by policy:file-intake-security
    - declared MIME equals detected MIME
    - declared MIME belongs to configured equivalent group for detected MIME
  reject:
    - declared MIME conflicts with detected MIME
    - detected or declared MIME is denied by data:security-policy-config
    - allow list exists and upload type is not allowed
    - prefix indicates script while script upload rejection is enabled and script family or extension is not explicitly allowed
    - prefix read fails before S3 upload starts
    - detected MIME unknown when strict mode enabled
  error:
    format: JSON
    status: 415
    code: content_type_mismatch
    fields:
      error: string
      message: string
  storage:
    rule: do not call system:s3-storage PutObject on reject
    replay: rule:prefix-replay
references:
  - requirement:streaming-upload
  - policy:file-intake-security
  - data:security-policy-config
  - rule:prefix-replay
  - system:content-detector
  - decision:mime-detector-library
  - api:upload-api
  - data:security-check-result
```
