---
id: policy:file-intake-security
type: policy
title: File Intake Security
---
File intake policy rejects risky uploads before durable storage whenever the decision can be made from bounded stream prefix inspection.

```yaml
rules:
  default_posture: whitelist
  prefix_inspection:
    read_limit_bytes: data:security-policy-config mime_magic.prefix_bytes default 3072
    checks:
      - system:content-detector lightweight type detection
      - magic number detection
      - known dangerous executable signatures
      - shebang and script language detection
      - archive or polyglot indicators when relevant
      - declared versus detected content type mismatch
    stream_handling:
      - read at most configured prefix bytes from http request body
      - detect type from prefix bytes only on request path
      - replay prefix before remaining body with rule:prefix-replay
      - send replayed stream to system:s3-storage only after allow decision
  declared_mime_consistency:
    enabled: default true, opt out through data:security-policy-config mime_magic.enabled false
    source_order:
      - create_upload_key content_type
      - upload request Content-Type header
    normalization:
      - parse media type and ignore parameters such as charset
      - compare canonical MIME essence values
      - allow configured equivalent groups for common aliases
      - allow built-in browser generic MIME compatibility before mismatch rejection:
          text/plain:
            includes: JSON, JSON suffix MIME, NDJSON, YAML, Python source MIME and Python MIME aliases
          text/markdown:
            includes: text/plain and text/html for Markdown files whose prefix looks like HTML
          detected_text_plain:
            includes: declared known text-derived MIME such as JSON, YAML, CSV, Markdown, HTML/XML, source text
            excludes: image, PDF, Office, archive, executable, opaque binary declarations
          application/octet-stream:
            includes: generic text fallback for text extensions plus Mach-O, ELF, ELF object/core/shared library, PE/MS portable executable and download MIME
          scope: MIME mismatch suppression only
          parser_policy: do not require parser validation during MIME consistency; downstream processors may parse strictly when they need structured semantics
      - allow extension fallback before mismatch rejection:
          markdown:
            extensions: [.md, .markdown]
            declared_detected_pairs:
              - [text/markdown, text/plain]
              - [text/markdown, text/html]
            required_followup: policy:markup-active-content-policy markdown inspection
          restructuredtext:
            extensions: [.rst, .rest]
            declared_detected_pairs:
              - [application/octet-stream, text/plain]
              - [text/plain, text/plain]
          makefile:
            basenames: [Makefile, GNUmakefile]
            script_family: make
          windows_script:
            extensions: [.bat, .cmd, .ps1, .psm1, .psd1]
            script_family:
              bat_cmd: batch
              ps: powershell
            required_followup: reject_script_uploads unless matching script family or extension is explicitly allowed
    filtering:
      config_source: data:security-policy-config
      allow_mime_types: optional whitelist, empty means no whitelist
      allow_file_types: category or short-name whitelist expanded to MIME types
      deny_mime_types: explicit reject list
      deny_file_types: category or short-name deny list expanded to MIME types
      equivalent_mime_types: configured alias groups
      generic_mime_compatibility: built-in mismatch-only mapping, not used for allow or deny matching
      extension_fallback: built-in mismatch-only and script-family mapping, not used for allow or deny matching
    reject_when:
      - declared MIME exists and detected MIME conflicts
      - detected or declared MIME matches deny list
      - allow list exists and neither detected nor declared MIME is allowed
      - detected MIME is unknown in strict mode
      - content appears executable, script, or archive disallowed by later policy
    error:
      http_status: 415
      code: content_type_mismatch
      response: JSON error
  allowlist:
    managed_by: configuration
    values:
      - media type
      - extension only as secondary hint
      - magic signature
  optional_scan:
    engine: system:clamav
    mode:
      - stream scan when available
      - asynchronous scan only when product accepts quarantine workflow
  reject:
    - executable formats unless explicitly allowed
    - shell script or other script uploaded under non-script media type
    - unknown type when whitelist mode is strict
    - file exceeding configured size limit
    - archive violating policy:archive-bomb-protection
    - file exceeding policy:resource-limit-policy
    - file failing policy:structural-validation-policy
    - file rejected by policy:file-type-sanitization-policy
    - Markdown, HTML, or XML rejected by policy:markup-active-content-policy
  references:
    - rule:prefix-replay
    - requirement:streaming-upload
    - requirement:mime-magic-consistency
    - data:security-policy-config
    - system:content-detector
    - decision:mime-detector-library
    - policy:archive-bomb-protection
    - policy:file-type-sanitization-policy
    - policy:resource-limit-policy
    - policy:structural-validation-policy
    - policy:document-active-content-policy
    - policy:svg-security-policy
    - policy:markup-active-content-policy
    - data:security-check-result
```
