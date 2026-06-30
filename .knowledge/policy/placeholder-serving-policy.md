---
id: policy:placeholder-serving-policy
type: policy
title: Placeholder Serving Policy
---
Placeholder serving policy supports both S3-stored placeholder objects and API-generated placeholders for pending derived assets.

```yaml
modes:
  s3_placeholder_object:
    behavior:
      - store shared placeholder images or documents in S3
      - return redirect or direct capability key when derived asset is pending
      - set placeholder object Cache-Control to no-store, no-cache, max-age=0 when freshness is required
    best_for:
      - simple CDN or static placeholder distribution
      - low API CPU
  api_generated_placeholder:
    behavior:
      - API returns placeholder bytes or SVG/PNG response
      - set Cache-Control: no-store, no-cache, max-age=0
      - optionally include Retry-After
    best_for:
      - tenant-specific placeholder
      - localized or branded placeholder
      - strict auth before placeholder response
default: api_generated_placeholder
constraints:
  - pending placeholder must not be cacheable unless URL is versioned by derived asset status
  - generated asset response may use normal cache headers after status becomes generated
references:
  - api:derived-asset-serving-api
  - data:derived-asset
```

