---
id: policy:processor-execution-policy
type: policy
title: Processor Execution Policy
---
Processor execution policy configures native and local command processors for upload-time and post-upload enrichment.

```yaml
processor:
  fields:
    name: stable id
    mode:
      enum:
        - native
        - local_command
        - openai_compatible_api
    timing:
      enum:
        - pre_accept
        - post_accept
        - on_demand
    required: boolean
    match:
      content_types: list optional
      roles: list optional
      max_size_bytes: integer optional
      extensions: list optional
    output:
      merge_path: JSON pointer optional
      artifact_kind: extracted_content or derived_asset optional
      max_stdout_bytes: integer
    failure:
      behavior:
        enum:
          - reject_upload
          - warn
          - mark_failed
          - skip
      retry: optional policy
    limits:
      timeout: duration
      concurrency_key: tenant, processor, global optional
      max_input_bytes: integer optional
timing_semantics:
  pre_accept:
    - runs before upload key reaches uploaded or clean state
    - required processor failure can reject upload
    - use only for fast checks or product-required facts before metadata submit
  post_accept:
    - runs after original object is durable
    - may run in worker queue beside preview, OCR, HLS, and extraction jobs
    - failure should usually warn or mark processor result failed
  on_demand:
    - runs when requested by UI/API and cached result is missing or stale
native_initial_scope:
  - Office metadata parsing
  - EXIF parsing with allowlisted fields
  - OCR when configured backend exists
  - image thumbnails
  - video thumbnails or animated previews
  - HLS generation
  - PDF or Office preview when converter exists
external_api_strategy:
  - OpenAI-compatible APIs may use system:openai-compatible-api-processor with configured prompt, endpoint, headers, and response JSON schema
  - non OpenAI-compatible provider APIs use system:local-command-processor for Google Vision, translation, AWS, GCP, and internal APIs
  - local command owns provider-specific auth, request shape, retry, and response normalization
openai_compatible_api:
  - endpoint URL and model are configured per processor
  - headers may include static values or environment-variable interpolation from allowlisted env vars
  - prompt can reference original file when explicitly allowed, data:extracted-content, data:processor-result, metadata fields, and derived preview images
  - response JSON must validate against configured schema before merge
  - suitable for OCR, image analysis, summary, classification, and custom JSON extraction
references:
  - system:local-command-processor
  - system:openai-compatible-api-processor
  - data:processor-result
  - data:openai-compatible-processor-config
  - policy:worker-queue-policy
  - policy:tool-backend-selection-policy
  - policy:search-extraction-policy
```
