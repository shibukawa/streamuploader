---
id: system:openai-compatible-api-processor
type: system
title: OpenAI Compatible API Processor
---
OpenAI compatible API processor calls configurable OpenAI-compatible HTTP APIs for OCR, image analysis, summaries, labels, and custom enrichment.

```yaml
capabilities:
  - OCR or visual text extraction from images or rendered pages
  - image analysis and labeling
  - extracted text summarization
  - document classification
  - metadata enrichment
  - custom JSON extraction from text or file-derived inputs
configuration:
  - data:openai-compatible-processor-config
input_materials:
  original_file:
    allowed_when: explicit policy allows sending file bytes to provider
    strategies:
      - inline base64 for small supported media
      - temporary scoped URL when provider can fetch and policy allows
      - rejected when privacy classification forbids external file bytes
  previous_results:
    examples:
      - data:extracted-content extracted_text
      - data:extracted-content ocr_text
      - data:processor-result metadata or labels
      - derived document preview page image
      - image thumbnail
prompt_binding:
  - render prompt templates with safe variables only
  - allow file facts, metadata fields, extracted text, OCR text, processor results, and bounded snippets
  - require size and token limits before request construction
  - keep raw secrets out of prompt variables
response_handling:
  - prefer OpenAI-compatible response_format json_schema when provider supports it
  - validate parsed JSON against configured JSON schema
  - reject or mark failed when schema validation fails
  - normalize output into data:processor-result
security:
  - endpoint allowlist or explicit configured URL required
  - no arbitrary user-supplied URLs
  - headers may interpolate only allowlisted environment variables
  - redact headers and prompt secrets from logs
  - audit provider base URL, model, request id, token usage, and output schema version when available
references:
  - data:openai-compatible-processor-config
  - data:processor-result
  - data:extracted-content
  - policy:processor-execution-policy
  - policy:external-delegation-policy
  - policy:search-extraction-policy
```
