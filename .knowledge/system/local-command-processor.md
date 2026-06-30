---
id: system:local-command-processor
type: system
title: Local Command Processor
---
Local command processor runs configured executables with stable JSON stdin and expects normalized JSON stdout.

```yaml
purpose:
  - provide extension point for APIs and tools streamuploader does not natively model
  - let operators use aws cli, gcloud, curl, python, node, or internal CLIs
  - keep API-specific auth, request shape, retry, and response normalization outside streamuploader core
invocation:
  mode: local_command
  command:
    argv: list of strings, shell string forbidden
    cwd: per-job temporary directory
    env: allowlisted variables and secrets only
  stdin_json:
    processor: processor name
    timing: policy:processor-execution-policy timing
    upload_key: data:file-item upload_key
    file:
      original_name: string
      content_type: string
      size_bytes: integer
      checksum_sha256: string optional
    storage:
      bucket: string
      object_key: string
      endpoint: configured internal endpoint optional
    local_file:
      path: optional temp path when input materialization is enabled
    metadata:
      existing_extracted: object optional
  stdout_json: data:processor-result
security:
  - absolute command path recommended in production
  - no shell interpolation
  - timeout required
  - stdout and stderr byte limits required
  - temporary directory removed after job unless debug retention enabled
  - network access is deployment responsibility, not streamuploader guarantee
  - command exit code nonzero maps to warning or failure according to processor config
references:
  - policy:processor-execution-policy
  - data:processor-result
  - system:external-tool-registry
  - policy:external-delegation-policy
```
