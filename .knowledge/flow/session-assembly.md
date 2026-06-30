---
id: flow:session-assembly
type: flow
title: Session Assembly
---
Session assembly watches or waits for independently uploaded keys and returns file facts for client-owned metadata submission.

```yaml
flow:
  trigger: client asks api:upload-api to watch or wait for upload keys
  steps:
    - name: issue_upload_keys
      actions:
        - create one data:file-item per selected file
        - allocate data:storage-key-allocation per file
        - return opaque upload_key, upload_url, display_key, object_key
        - create policy:work-sentinel-cleanup .work sentinel in system:s3-storage
    - name: receive_files
      parallel:
        uploads:
          - receive bytes through service endpoint, not direct S3
          - inspect prefix before forwarding bytes
          - run lightweight content detection and archive bomb checks
          - stream each file to system:s3-storage
          - run policy:file-intake-security per file
    - name: watch_progress
      actions:
        - client opens api:session-progress-api watch_websocket when incremental UI updates are needed
        - client sends watch messages with additional upload_keys as files are selected
        - server sends snapshot, progress, state, and error messages for watched keys
    - name: evaluate_readiness
      actions:
        - verify all requested upload_keys reached terminal state
        - mark batch ready when all requested files are uploaded and allowed
    - name: return_file_facts
      actions:
        - return display_key, object_key, content_type, size, checksum, and status per file
        - frontend submits application metadata plus returned file facts to system:application-server
  failure:
    missing_upload_key:
      - return status naming unknown or expired upload keys
    expired_session:
      - reject new bytes
      - cleanup abandoned objects by policy
    partial_upload_failure:
      - keep successful keys and return failed key statuses
    abandoned_session:
      - policy:work-sentinel-cleanup removes expired prefix
references:
  - api:upload-api
  - api:session-progress-api
  - data:upload-batch
  - data:file-item
  - data:storage-key-allocation
  - decision:upload-transport-boundary
  - decision:metadata-submit-trigger
  - requirement:application-metadata-submit
  - decision:derived-asset-readiness-policy
  - policy:preview-generation-policy
  - flow:image-thumbnail-generation
  - flow:document-preview-generation
  - flow:security-gated-upload-acceptance
  - policy:work-sentinel-cleanup
  - policy:storage-key-allocation-policy
```
