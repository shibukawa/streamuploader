---
id: flow:drag-drop-immediate-upload
type: flow
title: Drag Drop Immediate Upload
---
Drag and drop immediate upload starts file transfer as soon as files are selected, before final form submit.

```yaml
flow:
  trigger: user selects or drops files in client UI
  steps:
    - name: create_upload_keys
      actions:
        - create one upload key per dropped file
        - allocate storage folder and object key using policy:storage-key-allocation-policy
        - return folder plus file name key to client
        - attach client temporary id for UI correlation
    - name: upload_immediately
      actions:
        - start transfer to service content endpoint immediately
        - show per-file progress from browser upload progress and api:session-progress-api
        - allow metadata form editing while uploads continue
    - name: submit_metadata_later
      actions:
        - user submits form after metadata is ready
        - wait for required upload keys through api:upload-api
        - send metadata and returned file facts to system:application-server
  rationale:
    - input type file plus traditional submit delays file transfer until final submit
    - immediate upload hides large file transfer time behind form editing
    - user-visible wait at final submit is reduced
references:
  - api:upload-api
  - api:session-progress-api
  - policy:storage-key-allocation-policy
  - data:upload-batch
```
