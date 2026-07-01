---
id: data:logging-config
type: data
title: Logging Config
---
Logging config selects slog handler, level, and redaction behavior for access and audit events.

```yaml
source:
  env:
    format:
      - LOG_FORMAT
      - SLOG_FORMAT
    level:
      - LOG_LEVEL
  startup:
    - initialize default slog logger before listeners start
    - write logs to stdout for container log collectors
schema:
  logging:
    format:
      type: string
      default: text
      values:
        - text
        - json
      behavior:
        text: use slog.NewTextHandler
        json: use slog.NewJSONHandler
    level:
      type: string
      default: info
      values:
        - debug
        - info
        - warn
        - error
    redact:
      type: bool
      default: true
      meaning: redact bearer tokens, shared keys, auth headers, and storage credentials
events:
  access:
    - method
    - path_template or route
    - status
    - duration_ms
    - request_id
    - source_ip
    - user_agent optional
  audit:
    - event
    - decision
    - reason_code
    - upload_key optional
    - object_key optional
    - shared_key_hash optional
startup_warnings:
  - warn when policy:shared-key-policy default_ttl is empty while shared keys are enabled
  - warn when policy:http-cors-header-policy allowed_origins contains wildcard
startup_info:
  - log data:http-cache-config effective mode, max_age, s_maxage, ETag forwarding, and Last-Modified forwarding
  - log selected slog handler and level
references:
  - policy:audit-log-policy
  - policy:shared-key-policy
  - policy:http-cors-header-policy
  - data:http-cache-config
```
