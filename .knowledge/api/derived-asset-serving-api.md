---
id: api:derived-asset-serving-api
type: api
title: Derived Asset Serving API
---
Derived asset serving API returns generated assets through api:download-api file routes when ready and no-cache placeholders while generation is pending.

```yaml
endpoints:
  get_derived_asset:
    method: GET
    path: /api/file/{key}/variants/{variant_key}/content
    behavior:
      generated:
        - return generated object or redirect/proxy to object storage
        - use normal cache headers according to asset policy
      pending:
        - return placeholder from policy:placeholder-serving-policy
        - set Cache-Control: no-store, no-cache, max-age=0
        - set Retry-After or status hint when useful
      failed:
        - return failure placeholder
        - set Cache-Control: no-store, no-cache, max-age=0
  status:
    method: GET
    path: "{api:upload-api base_path}/keys/{upload_key}"
    behavior:
      - exposes data:derived-asset status
  thumbnail:
    method: GET
    path: /api/file/{key}/thumbnail
    alternate_path: /api/file/{key}/thumbnail/{preset}
    behavior:
      - return thumbnail data:derived-asset when ready
      - return placeholder while pending when policy:placeholder-serving-policy permits
      - default preset resolves object key suffix /thumbnail
      - set content type from generated data:derived-asset
references:
  - data:derived-asset
  - data:thumbnail-generation-config
  - api:session-progress-api
  - api:download-api
  - policy:placeholder-serving-policy
  - policy:object-access-policy
```
