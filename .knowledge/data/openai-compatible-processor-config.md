---
id: data:openai-compatible-processor-config
type: data
title: OpenAI Compatible Processor Config
---
OpenAI compatible processor config defines a prompt-driven HTTP processor for commodity OpenAI-compatible APIs.

```yaml
fields:
  name: stable processor id
  mode: openai_compatible_api
  endpoint:
    base_url: string
    path: string default /v1/chat/completions
    model: string
    timeout: duration
  headers:
    static: map optional
    from_env:
      header_name: env var name
    interpolation:
      syntax: ${ENV_NAME}
      allowed_env_prefixes: configurable
      missing_behavior: fail startup or disable processor
  request:
    temperature: number optional
    max_tokens: integer optional
    response_format:
      type: json_schema preferred
      schema: JSON Schema object
      strict: boolean default true
    prompt:
      system: string optional
      developer: string optional
      user_template: string
    inputs:
      - source: original_file
        modes:
          - inline_base64_when_small
          - temporary_url_when_allowed
          - text_only_for_unsupported_file
      - source: data:extracted-content
      - source: data:processor-result
      - source: metadata fields
      - source: derived image thumbnail or rendered document page
  output:
    validates_against: response JSON schema
    destination:
      - data:processor-result metadata
      - data:processor-result text
      - data:extracted-content
      - derived asset metadata
  privacy:
    require_explicit_opt_in_for_file_bytes: true
    allow_text_result_input: configurable
    allow_image_input: configurable
    allow_original_file_input: configurable
references:
  - system:openai-compatible-api-processor
  - data:processor-result
  - data:extracted-content
  - policy:processor-execution-policy
```
