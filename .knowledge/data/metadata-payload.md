---
id: data:metadata-payload
type: data
title: Metadata Payload
---
Metadata payload is application-owned client JSON enriched with streamuploader file facts.

```yaml
shape:
  client_metadata: object
  upload_batch:
    batch_key: string optional
  files:
    type: list of data:file-item summaries
    include:
      - display_key
      - object_key
      - storage_prefix
  derived_assets:
    type: list of data:derived-asset summaries when generated or required
  security:
    aggregate_decision: allow or reject
    per_file_results: list
validation:
  - system:application-server rejects unknown or oversized metadata fields according to app contract
  - never trust client-supplied content type without prefix inspection
references:
  - requirement:application-metadata-submit
  - policy:file-intake-security
  - data:upload-batch
  - data:derived-asset
```
