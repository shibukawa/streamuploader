---
id: decision:derived-asset-readiness-policy
type: decision
title: Derived Asset Readiness Policy
---
Preview generation should not block original file upload acceptance by default; pending previews are represented by no-cache placeholders.

```yaml
decision:
  default: async_after_original_upload
  rationale:
    - preview failures should not block generic file upload flows
    - image and document processing can be slower and more failure-prone than storage
    - application metadata can use original file keys while previews are generated
  modes:
    async_optional:
      behavior:
        - flow:security-gated-upload-acceptance completes original file acceptance first
        - preview generation runs after or beside original file readiness
        - pending requests return api:derived-asset-serving-api placeholder
    later_application_update:
      behavior:
        - frontend may submit metadata with original file keys
        - application may later patch metadata with derived asset keys if product needs it
    required_before_metadata_submit:
      status: discouraged
      behavior:
        - use only when application cannot accept original file without preview
references:
  - flow:image-thumbnail-generation
  - flow:document-preview-generation
  - policy:preview-generation-policy
  - flow:security-gated-upload-acceptance
  - api:derived-asset-serving-api
  - data:derived-asset
  - requirement:application-metadata-submit
```
