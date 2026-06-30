---
id: policy:search-extraction-policy
type: policy
title: Search Extraction Policy
---
Search extraction policy controls text, metadata, and OCR extraction for search engine ingestion.

```yaml
selector:
  - detected content type
  - file role
  - tenant policy
  - upload policy
  - application metadata requirements
outputs:
  extracted_text:
    mode: optional
    flow: flow:text-extraction-generation
  extracted_metadata:
    mode: optional
    flow: flow:text-extraction-generation
  ocr_text:
    mode: opt_in
    flow: flow:ocr-extraction-generation
data_type_rules:
  text_like:
    extract_text: true
    ocr: false
  pdf:
    extract_text: true
    ocr: when no embedded text or policy requests
  office_document:
    extract_text: true
    ocr: false unless rendered pages need OCR
  image:
    extract_metadata: true
    ocr: opt_in
  video_audio:
    extract_metadata: true
    ocr: false
privacy:
  - EXIF/GPS/author/revision/comment metadata may be sensitive
  - separate metadata stripping for derived assets from metadata extraction for search
  - allow fields denylist and allowlist before index handoff
  - support do_not_index classification
index_handoff:
  - store extracted artifact in S3 first
  - send object key, checksum, language, and chunk descriptors to search pipeline when configured
  - do not block upload acceptance unless product policy requires indexing before metadata submit
external_delegation:
  - allowed only through policy:external-delegation-policy
  - external APIs are invoked through system:local-command-processor, not generic webhooks
  - OpenAI-compatible APIs may use system:openai-compatible-api-processor when prompt, headers, and response schema are configured
  - local and native extractors are preferred for private content
  - cloud document AI, vision, and translation commands are opt-in per tenant and file role
processors:
  - selected processors follow policy:processor-execution-policy
  - required pre_accept extraction can block upload acceptance only when product policy needs facts before metadata submit
  - OpenAI-compatible processor output may summarize, classify, analyze images, or produce OCR-like JSON when configured
  - post_accept extraction should write data:extracted-content or data:processor-result artifacts asynchronously
limits:
  - maximum extracted bytes
  - maximum characters
  - maximum pages
  - maximum OCR pages
  - maximum metadata fields
references:
  - data:extracted-content
  - flow:text-extraction-generation
  - flow:ocr-extraction-generation
  - system:text-extractor
  - system:ocr-engine
  - system:external-processing-delegates
  - system:local-command-processor
  - system:openai-compatible-api-processor
  - policy:external-delegation-policy
  - policy:processor-execution-policy
  - policy:metadata-stripping-policy
```
