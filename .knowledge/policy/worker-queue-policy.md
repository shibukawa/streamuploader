---
id: policy:worker-queue-policy
type: policy
title: Worker Queue Policy
---
Worker queue policy manages asynchronous processors for previews, media transforms, extraction, compression variants, local command jobs, webhooks, and retries.

```yaml
jobs:
  - image_thumbnail
  - svg_preview
  - document_preview
  - video_preview
  - hls_transcode
  - download_variant
  - text_extraction
  - metadata_extraction
  - ocr_extraction
  - metadata_strip
  - local_command_processor
  - webhook_delivery
behavior:
  - idempotent job keys
  - create data:async-task-marker when asynchronous work starts
  - delete data:async-task-marker when work reaches terminal success or terminal failure
  - timing follows policy:processor-execution-policy pre_accept, post_accept, or on_demand
  - selected backend recorded from system:external-tool-registry
  - delegated provider recorded from system:external-processing-delegates when external delegation is used
  - local command stdout data:processor-result is validated before merge
  - retry with backoff
  - terminal failure state
  - dead letter queue or dead letter prefix
  - per-tenant concurrency limits
  - CPU and wall-clock budgets
status:
  - queued
  - running
  - retrying
  - succeeded
  - failed_terminal
references:
  - flow:image-thumbnail-generation
  - flow:document-preview-generation
  - flow:video-preview-generation
  - flow:hls-generation
  - flow:download-variant-generation
  - flow:text-extraction-generation
  - flow:ocr-extraction-generation
  - system:external-tool-registry
  - policy:tool-backend-selection-policy
  - policy:search-extraction-policy
  - policy:external-delegation-policy
  - policy:processor-execution-policy
  - system:local-command-processor
  - data:async-task-marker
  - api:async-task-wait-api
```
