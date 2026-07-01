---
id: data:upload-deadline-config
type: data
title: Upload Deadline Config
---
Upload deadline config controls S3-backed upload key start and finish deadlines plus cleanup execution.

```yaml
source:
  yaml:
    location: service config or security policy yaml if implementation keeps a single policy file
    section: upload_deadlines
    validation:
      - fail startup for unknown keys
      - durations must be positive when enabled
  env_overrides:
    finish_deadline:
      - UPLOAD_FINISH_TIMEOUT
      - UPLOAD_FINISH_TIMEOUT_SECONDS
    cleanup_interval:
      - UPLOAD_CLEANUP_INTERVAL
      - UPLOAD_CLEANUP_INTERVAL_SECONDS
schema:
  upload_deadlines:
    enabled:
      type: bool
      default: true
    marker_prefix:
      type: string
      default: .uploading/
    start_timeout:
      type: duration
      default: 10s
      meaning: max delay between key creation and first upload byte acceptance
    finish_timeout:
      type: duration
      default: 1m
      meaning: max wall-clock duration from key creation or configured start point to upload completion
      env_override: true
    cleanup:
      enabled:
        type: bool
        default: true
        meaning: run in-process cleanup loop when server is long-lived
      interval:
        type: duration
        default: 1m
        env_override: true
      startup_run:
        type: bool
        default: true
      command_mode:
        type: string
        values:
          - server_loop
          - cleanup_once
          - disabled
        meaning: cleanup_once runs cleanup and exits for cloud scheduler or batch worker deployments
example:
  upload_deadlines:
    enabled: true
    marker_prefix: .uploading/
    start_timeout: 10s
    finish_timeout: 1m
    cleanup:
      enabled: true
      interval: 1m
      startup_run: true
      command_mode: server_loop
references:
  - policy:upload-key-deadline-policy
  - policy:work-sentinel-cleanup
```
