---
id: data:processor-result
type: data
title: Processor Result
---
Processor result is normalized JSON returned by native or local command processors and merged into file facts.

```yaml
shape:
  metadata:
    type: object optional
    examples:
      - exif
      - office
      - labels
      - translated_text
      - openai_compatible_analysis
      - summary
      - classification
      - custom provider normalized fields
  text:
    type: object optional
    fields:
      plain: string optional
      language: BCP47 tag optional
      pages: list optional
      chunks: list optional
  derived_assets:
    type: list of data:derived-asset summaries optional
  warnings:
    type: list
    item:
      code: string
      message: string
  errors:
    type: list
    item:
      code: string
      message: string
merge:
  - processor config declares merge_path or artifact destination
  - streamuploader validates JSON size and allowed top-level fields
  - command-specific provider responses must be normalized before stdout
  - OpenAI-compatible processor responses must validate against configured JSON schema before merge
  - raw provider responses may be stored only when retention policy allows
references:
  - system:local-command-processor
  - system:openai-compatible-api-processor
  - data:openai-compatible-processor-config
  - data:extracted-content
  - data:derived-asset
  - policy:processor-execution-policy
```
