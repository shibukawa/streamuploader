---
id: data:http-cache-config
type: data
title: HTTP Cache Config
---
HTTP cache config controls file access cache headers and startup visibility.

```yaml
source:
  config:
    section: http_cache
  env_overrides:
    mode:
      - HTTP_CACHE_MODE
    max_age:
      - HTTP_CACHE_MAX_AGE
      - HTTP_CACHE_MAX_AGE_SECONDS
schema:
  http_cache:
    mode:
      type: string
      default: private
      values:
        - private
        - public
        - no-store
      behavior:
        private: Cache-Control private with max-age when configured
        public: Cache-Control public with max-age and s-maxage
        no-store: Cache-Control no-store
    max_age:
      type: duration
      default: 24h
      meaning: used for private and public cache modes unless endpoint overrides it
    s_max_age:
      type: duration or empty
      default: empty
      meaning: shared cache max age for public mode; empty means same as max_age
    validators:
      etag:
        default: true
        meaning: forward storage ETag when available
      last_modified:
        default: true
        meaning: forward storage Last-Modified when available
startup_info:
  - log configured cache mode at info level
  - log max_age and effective s_maxage at info level
  - log whether ETag and Last-Modified forwarding are enabled
rules:
  - X-Content-Type-Options: nosniff is always emitted on file access responses
  - placeholder and pending derived responses may override to no-store through policy:placeholder-serving-policy
  - no-store mode emits no positive max-age
  - public mode emits s-maxage equal to max-age by default
references:
  - api:download-api
  - policy:http-cors-header-policy
  - policy:placeholder-serving-policy
```
