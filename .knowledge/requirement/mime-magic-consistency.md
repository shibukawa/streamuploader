---
id: requirement:mime-magic-consistency
type: requirement
title: MIME Magic Consistency
---
Uploads must verify declared MIME type against magic-header detection before writing object bytes to durable storage.

```yaml
requirements:
  scope:
    first_phase: declared MIME versus detected MIME from bounded prefix
    later_phase:
      - gzip bomb and archive expansion policy:archive-bomb-protection
      - allowlist and denylist filtering from configuration
      - deeper malware scan through system:clamav when enabled
  input:
    declared_content_type:
      - data:file-item content_type from create key request
      - upload request Content-Type header fallback
    body: streaming HTTP request body
  prefix_read:
    limit_bytes: data:security-policy-config mime_magic.prefix_bytes default 3072
    memory: bounded byte slice only, never full file
    eof_before_limit: valid small upload, inspect available bytes
  configuration:
    env:
      path: SU_SECURITY_CONFIG
      legacy_fallback: SECURITY_CONFIG accepted for compatibility only
    file_format: YAML
    startup: load once before serving requests
    validation:
      - validate YAML against built-in JSON Schema before unmarshalling
      - reject unknown allow_file_types and deny_file_types keys
      - reject unknown allow_mime_types and deny_mime_types keys
      - reject unknown allowed_script_types and allowed_script_extensions keys
      - reject legacy list syntax for type switches
    defaults:
      mime_magic.enabled: true
      mime_magic.prefix_bytes: 3072
      mime_magic.reject_script_uploads: true
    lists:
      allow_file_types: bool switch map for aliases such as images, png, jpeg, pdf
      allow_mime_types: optional accept list for large future policy sets
      deny_file_types: bool switch map for aliases such as exe or archives
      deny_mime_types: optional reject list checked before allow
      allowed_script_types: opt-in bool switch map for shell, python, node, ruby, perl, php, powershell, batch, make
      allowed_script_extensions: opt-in bool switch map for sh, py, js, rb, pl, php, ps1, bat, cmd, Makefile
      equivalent_mime_types: alias groups for MIME comparison
  detection:
    primary: decision:mime-detector-library
    fallback: Go net/http DetectContentType for unknown primary result
    compare: normalized MIME essence, without parameters
  browser_declared_mime_context:
    standard:
      file_api:
        rule: File.type may be a parsable MIME string or empty string when type cannot be determined
        limits:
          - user agents must return lower-case MIME essence
          - text/plain must not include charset parameter
          - exact extension-to-MIME map is implementation and OS dependent
        source: https://w3c.github.io/FileAPI/#file-type-guidelines
      mdn_observation:
        rule: browsers generally do not read file bytes for File.type; they infer from extension and client configuration
        examples:
          renamed_png_to_txt: text/plain, not image/png
          uncommon_extension: empty string is common
          windows_registry: may change even common type results
        source: https://developer.mozilla.org/en-US/docs/Web/API/Blob/type
    browser_families:
      chromium_based:
        examples: Chrome, Edge, Chromium
        likely_behavior:
          - extension or platform MIME database determines File.type
          - source/code files with common text extensions may be text/plain or empty instead of language-specific MIME
          - unknown binary files may be empty or application/octet-stream depending on construction/upload path
      gecko_based:
        examples: Firefox
        likely_behavior:
          - extension or platform MIME database determines File.type
          - uncommon developer files may be empty, text/plain, or a platform-specific MIME
      webkit_based:
        examples: Safari
        likely_behavior:
          - extension or platform MIME database determines File.type
          - macOS UTI mapping may influence text and executable file types
          - Markdown may be declared text/markdown while prefix detector returns text/html when leading HTML-like markup exists
          - reStructuredText may be declared application/octet-stream while prefix detector returns text/plain
    product_policy:
      assumption: client-declared MIME is a hint, not authoritative validation input
      reason: browser-specific and OS-specific declared MIME variance must not fail otherwise valid uploads before server-side prefix detection
  extension_fallback:
    purpose: recover declared-versus-detected compatibility for text formats whose bytes are intentionally generic
    precedence:
      - never override executable magic signatures
      - never bypass deny_mime_types or deny_file_types
      - never bypass policy:markup-active-content-policy
      - never bypass reject_script_uploads; extension script fallback feeds script detection
    normalized_name:
      extension: lowercase last filename extension without leading dot
      basename: lowercase path base for extensionless well-known names
    text_markup:
      markdown:
        extensions: [md, markdown]
        declared: [text/markdown]
        compatible_detected: [text/plain, text/html]
        reason: Markdown may begin with HTML blocks and be detected as text/html
        required_followup: policy:markup-active-content-policy markdown inspection still required
      restructuredtext:
        extensions: [rst, rest]
        declared: [application/octet-stream, text/plain]
        compatible_detected: [text/plain]
        reason: Safari may send rst as application/octet-stream while prefix detector reports generic text
        required_followup: treat as text document; no active HTML bypass
      plain_text:
        extensions: [txt, text]
        declared: [application/octet-stream]
        compatible_detected: [text/plain]
        reason: opaque browser declaration can still be generic plain text
    script_like:
      makefile:
        basenames: [makefile, gnumakefile]
        compatible_detected: [text/plain]
        script_family: make
        detection_note: content detector need not prove make syntax from bytes
      windows_batch:
        extensions: [bat, cmd]
        compatible_detected: [text/plain]
        script_family: batch
        detection_note: extension may classify script family before content detector proves batch syntax
      powershell:
        extensions: [ps1, psm1, psd1]
        compatible_detected: [text/plain]
        script_family: powershell
        detection_note: extension may classify script family before content detector proves PowerShell syntax
    accept_condition: extension fallback may suppress content_type_mismatch only when declared and detected MIME are in the format-specific compatible sets
  extension_content_type_match:
    purpose: ensure accepted files with known extensions match the type represented by that extension
    source: data:security-policy-config allow_file_types short-name MIME map
    scope:
      - last filename extension after normalization
      - known extensions only; unknown extensions are not interpreted as type policy
    accept:
      - detected MIME matches extension expected MIME or configured equivalent group
      - declared MIME matches extension expected MIME and detected MIME is empty, application/octet-stream, equivalent, or browser-generic compatible
      - generic text detection for JSON/YAML/CSV/Markdown/XML/source extensions when declared MIME matches the extension type
    reject:
      - detected concrete MIME belongs to another known family than the extension
      - declared MIME and detected MIME both fail the extension expected MIME set
    error:
      status: 415
      code: file_extension_mismatch
  archive_entry_content_type_match:
    purpose: apply extension_content_type_match to files accepted inside uploaded zip, tar, and 7z archives
    trigger: policy:archive-bomb-protection inspects zip central directory, tar headers, or 7z headers
    scope:
      - non-directory zip entries
      - regular tar entries
      - non-directory 7z entries
      - entries with safe paths and known last filename extension
      - entry declared size already accepted by policy:archive-bomb-protection
      - entry bytes read through bounded prefix only
    accept:
      - detected entry MIME matches extension expected MIME or configured equivalent group
      - detected generic text/plain is compatible with a text-derived extension such as json, csv, markdown, xml, or txt
      - unknown entry extension is skipped
    reject:
      - known entry extension conflicts with detected concrete MIME
      - entry content type cannot be detected for a known extension
      - entry name or shebang indicates script while script upload rejection is enabled and script family or extension is not explicitly allowed
    error:
      status: 415
      codes:
        - archive_entry_type_mismatch
        - archive_entry_script_rejected
  generic_text_detected_fallback:
    purpose: accept browser-declared structured text when bounded prefix detection can only prove generic text/plain
    rule: if detected MIME is text/plain and declared MIME is known text-derived MIME, suppress content_type_mismatch
    no_parser_on_upload:
      reason: MIME consistency check should not require full parser validation for JSON, YAML, CSV, Markdown, or source text
      parser_validation_scope: only later processing, extraction, preview, or structural validation may parse strictly when needed
    allowed_declared_mime:
      structured_data:
        - application/json
        - application/*+json
        - application/yaml
        - application/x-yaml
        - text/yaml
        - text/x-yaml
        - text/csv
      markup_text:
        - text/markdown
        - text/html
        - application/xhtml+xml
        - application/xml
        - text/xml
        - application/*+xml
      source_text:
        - text/x-python
        - application/x-python
        - text/x-script.python
        - text/javascript
        - application/javascript
        - text/x-shellscript
        - text/x-ruby
        - text/x-perl
        - application/x-httpd-php
        - text/x-powershell
        - text/x-makefile
        - application/x-bat
      generic_text:
        - text/plain
    exclusions:
      - do not treat text/plain detection as compatible with image, audio, video, PDF, Office, archive, executable, or opaque binary declarations
      - do not bypass policy:markup-active-content-policy for Markdown, HTML, XML, XHTML, or XML suffix types
      - do not bypass reject_script_uploads for script family MIME or extension-detected scripts
  accept:
    - declared MIME missing and detected MIME allowed by policy:file-intake-security
    - declared MIME equals detected MIME
    - declared MIME belongs to configured equivalent group for detected MIME
    - declared browser generic MIME is input-compatible with detected specific MIME by built-in mismatch-only compatibility:
        text/plain:
          - application/json
          - application/*+json
          - application/x-python
          - application/x-ndjson
          - application/yaml
          - application/x-yaml
          - text/yaml
          - text/x-yaml
          - text/x-python
          - text/x-script.python
        text/markdown:
          - text/plain
          - text/html
        application/json:
          - text/plain
        application/*+json:
          - text/plain
        application/yaml:
          - text/plain
        application/x-yaml:
          - text/plain
        text/yaml:
          - text/plain
        text/x-yaml:
          - text/plain
        application/octet-stream:
          - text/plain
          - application/vnd.microsoft.portable-executable
          - application/x-coredump
          - application/x-dosexec
          - application/x-elf
          - application/x-executable
          - application/x-mach-binary
          - application/x-msdownload
          - application/x-object
          - application/x-sharedlib
      note: compatibility suppresses content_type_mismatch only; deny lists and script rejection still apply.
  reject:
    - declared MIME conflicts with detected MIME
    - known filename extension conflicts with detected and declared MIME
    - known zip, tar, or 7z entry extension conflicts with detected entry MIME
    - known zip, tar, or 7z entry appears script-like and scripts are not explicitly allowed
    - detected or declared MIME is denied by data:security-policy-config
    - allow list exists and upload type is not allowed
    - prefix indicates script while script upload rejection is enabled and script family or extension is not explicitly allowed
    - prefix read fails before S3 upload starts
    - detected MIME unknown when strict mode enabled
  error:
    format: JSON
    status: 415
    code: content_type_mismatch
    fields:
      error: string
      message: string
  storage:
    rule: do not call system:s3-storage PutObject on reject
    replay: rule:prefix-replay
references:
  - requirement:streaming-upload
  - policy:file-intake-security
  - data:security-policy-config
  - rule:prefix-replay
  - system:content-detector
  - decision:mime-detector-library
  - api:upload-api
  - data:security-check-result
```
