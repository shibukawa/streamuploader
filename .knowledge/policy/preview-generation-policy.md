---
id: policy:preview-generation-policy
type: policy
title: Preview Generation Policy
---
Preview generation policy decides whether and how derived assets are created per detected data type and file role.

Search-oriented text, metadata, and OCR outputs are governed separately by policy:search-extraction-policy because they affect indexing and privacy.

```yaml
policy:
  selector:
    - detected content type
    - file role
    - upload policy
    - application metadata requirements
  defaults:
    mode: optional
    generate: false
    image_thumbnail_enabled: data:thumbnail-generation-config enabled default false
  data_type_rules:
    image:
      generate: data:thumbnail-generation-config enabled
      mode: sequential or async from data:thumbnail-generation-config execution_mode
      flow: flow:image-thumbnail-generation
      source_priority:
        - embedded thumbnail
        - generated full-image thumbnail
      outputs:
        - image_thumbnail
    svg:
      generate: configurable
      mode: optional
      flow: flow:svg-preview-generation
      outputs:
        - image_thumbnail
    pdf:
      generate: configurable
      mode: optional
      flow: flow:document-preview-generation
      outputs:
        - document_preview_page
    office_document:
      generate: configurable
      mode: optional
      flow: flow:document-preview-generation
      source_priority:
        - embedded Office thumbnail
        - LibreOffice PDF normalization and rendered page thumbnail
      outputs:
        - document_preview_page
        - normalized_pdf optional
    video:
      generate: configurable
      mode: optional
      flow: flow:video-preview-generation
      source_priority:
        - attached picture or cover art
        - scored early keyframe still thumbnail
        - animated preview clip
      outputs:
        - video_still_thumbnail
        - video_animated_preview
    other:
      generate: false
  readiness_modes:
    optional:
      - upload acceptance can proceed after original file acceptance
      - derived assets appear later in status
    async_thumbnail:
      - wait endpoint completes on original upload readiness
      - thumbnail asset may still be pending
    sequential_thumbnail:
      - wait endpoint completes only after thumbnail terminal state
    required_before_metadata_submit:
      - upload batch readiness requires generated assets
      - frontend metadata payload includes generated asset keys
    later_application_update:
      - frontend submits original file keys first
      - application later records derived asset keys when supported
references:
  - data:derived-asset
  - data:thumbnail-generation-config
  - policy:preview-format-policy
  - flow:image-thumbnail-generation
  - flow:svg-preview-generation
  - flow:document-preview-generation
  - flow:video-preview-generation
  - requirement:expanded-thumbnail-source-support
  - policy:search-extraction-policy
```
