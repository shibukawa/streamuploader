---
id: flow:download-variant-generation
type: flow
title: Download Variant Generation
---
Download variant generation precomputes compressed delivery objects for files that benefit from bandwidth reduction.

```yaml
flow:
  trigger: data:file-item uploaded, accepted, and selected by policy:download-variant-policy
  steps:
    - name: classify_compressibility
      actions:
        - use detected content type and optional sampling
        - skip already compressed formats
        - skip image, audio, video, archive, and zipped Office containers by default
        - estimate benefit versus CPU cost
    - name: compress_variant
      actions:
        - run zstd worker with configured level and time limits
        - enforce max output size and ratio sanity
        - preserve original content type
    - name: store_or_promote
      actions:
        - keep original and write separate variant when policy chooses keep_original_and_generate_variant
        - rewrite same object key with compressed bytes when policy chooses replace_original_same_key
        - write variant then delete original when policy chooses promote_variant_delete_original
        - set Content-Encoding only on compressed objects
        - record data:derived-asset entry or update file canonical key
    - name: expose_variant
      actions:
        - include variant metadata in status and metadata payload when useful
        - serving layer selects variant by Accept-Encoding
  failure:
    optional_variant:
      - keep original file accepted
      - mark variant skipped or failed
references:
  - policy:download-variant-policy
  - data:derived-asset
  - system:s3-storage
```
