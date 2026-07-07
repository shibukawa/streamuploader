---
id: api:extracted-content-api
type: api
title: Extracted Content API
---
Extracted content API lets backend callers read normalized text extraction JSON for accepted objects.

```yaml
base_path: /internal
endpoints:
  get_extracted_content:
    method: GET
    path: /internal/objects/{object_key}/extracted-content
    authorization: policy:backend-control-plane-policy
    query:
      wait:
        type: boolean
        default: false
        meaning: wait for selected extraction task markers before reading artifact
      timeout_seconds:
        type: integer optional
        default: 60 when wait true
      include:
        type: repeated string or comma-separated list
        optional: true
        default:
          - text
          - extracted
          - title
          - description
          - ocr
        allowed:
          - text
          - extracted
          - title
          - description
          - ocr
          - metadata
          - sources
      status_only:
        type: boolean
        default: false
        meaning: return task and artifact status without body text
    behavior:
      - derive default artifact object key as source object key plus .text.json
      - when wait true, apply api:async-task-wait-api semantics for text_extraction, metadata_extraction, and ocr_extraction task markers
      - return 200 with data:extracted-content JSON when artifact exists
      - return 202 when selected tasks are still pending and wait is false or timeout fires
      - return 204 or 404 by configured policy when no extraction was scheduled and artifact is absent
      - never generate extraction synchronously inside read request unless policy:processor-execution-policy explicitly enables on_demand work
      - filter texts, metadata, and sources according to include query before response
      - preserve data:extracted-content texts map shape such as {"extracted":"...", "ocr":"..."}
    response:
      object_key: source object key
      artifact_object_key: source object key plus .text.json by default
      status:
        enum:
          - generated
          - pending
          - skipped
          - failed
          - not_scheduled
      content: data:extracted-content optional unless status_only true
      tasks:
        - object_key: source object key
          kind: text_extraction, metadata_extraction, or ocr_extraction
          pending: boolean
      error_code: optional
  create_extracted_content_presigned_url:
    method: POST
    path: /internal/objects/{object_key}/extracted-content/presigned-url
    authorization: policy:backend-control-plane-policy
    body:
      ttl_seconds: optional bounded TTL
      wait: boolean default false
      include_pending_status: boolean default true
    behavior:
      - derive artifact object key as source object key plus .text.json
      - optionally wait for extraction markers before signing
      - return presigned GET URL only when artifact exists and caller is authorized to read extracted content
      - use api:download-api create_presigned_url internally or equivalent storage operation
    response:
      artifact_object_key: string
      url: S3 presigned GET URL
      expires_at: timestamp
      status: generated or pending
constraints:
  - backend-only; no browser CORS
  - upload capability tokens cannot read extracted content
  - response may contain private file text and metadata, so apply tenant ownership, namespace, and privacy classification checks
  - do_not_index or internal_only content is still protected by backend authorization and may be hidden from callers without extracted-content read permission
  - large text responses may be truncated only when explicitly requested; default API returns stored JSON as-is within response size policy
references:
  - data:extracted-content
  - data:async-task-marker
  - api:async-task-wait-api
  - api:download-api
  - policy:backend-control-plane-policy
  - policy:search-extraction-policy
  - policy:processor-execution-policy
  - system:s3-storage
```
