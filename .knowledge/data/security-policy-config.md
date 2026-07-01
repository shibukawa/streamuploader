---
id: data:security-policy-config
type: data
title: Security Policy Config
---
Security policy config is a YAML file loaded at streamuploader startup for file intake filtering.

```yaml
source:
  env_path:
    primary: SU_SECURITY_CONFIG
    legacy_compat: SECURITY_CONFIG
  read_time: process startup before listening
  missing_path: use built-in defaults
  invalid_yaml: fail startup
  validation:
    method: built-in JSON Schema before unmarshalling policy
    unknown_keys: fail startup
    wrong_types: fail startup
schema:
  mime_magic:
    enabled:
      type: bool
      default: true
      meaning: opt-out switch for declared MIME versus magic-header check
    prefix_bytes:
      type: integer
      default: 3072
      constraints:
        min: 512
        max: 1048576
    reject_script_uploads:
      type: bool
      default: true
    allowed_script_types:
      type: map string bool
      default: {}
      meaning: opt-in accepted script families detected from shebang
      example:
        shell: true
        python: true
    allowed_script_extensions:
      type: map string bool
      default: {}
      meaning: opt-in accepted script filename extensions when extension policy is desired
      example:
        sh: true
        py: true
    allow_mime_types:
      type: map string bool
      default: {}
      meaning: if non-empty, detected or declared MIME must be in list or equivalent group
      example:
        application/pdf: true
    allow_file_types:
      type: map string bool
      default: {}
      meaning: category or short file type aliases expanded to allow_mime_types
      example:
        images: true
        pdf: true
      values:
        categories:
          - images
          - documents
          - archives
          - audio
          - videos
          - text
        examples:
          - png
          - jpeg
          - pdf
          - csv
          - zip
    deny_mime_types:
      type: map string bool
      default: {}
      meaning: detected or declared MIME in list is rejected before allow checks
      example:
        application/x-executable: true
    deny_file_types:
      type: map string bool
      default: {}
      meaning: category or short file type aliases expanded to deny_mime_types
      example:
        exe: true
    equivalent_mime_types:
      type: list of list string
      default:
        - [application/xml, text/xml]
        - [image/jpeg, image/pjpeg]
        - [application/gzip, application/x-gzip]
example:
  mime_magic:
    enabled: true
    prefix_bytes: 3072
    reject_script_uploads: true
    allowed_script_types:
      shell: false
      python: false
    allowed_script_extensions:
      sh: false
      py: false
    allow_file_types:
      images: true
      pdf: true
    allow_mime_types:
      text/plain: true
    deny_file_types:
      exe: true
    deny_mime_types:
      application/x-executable: true
      application/x-sharedlib: true
      application/x-msdownload: true
    equivalent_mime_types:
      - [image/jpeg, image/pjpeg]
      - [application/gzip, application/x-gzip]
references:
  - policy:file-intake-security
  - requirement:mime-magic-consistency
  - decision:mime-detector-library
```
