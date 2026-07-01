---
id: system:content-detector
type: system
title: Content Detector
---
Content detector performs lightweight file type and script detection from bounded prefixes and small structured probes.

```yaml
components:
  baseline:
    - Go net/http DetectContentType for first 512 bytes
  go_native:
    - github.com/gabriel-vasile/mimetype for primary prefix MIME detection
    - h2non/filetype for fast magic signature detection
  optional_deeper:
    - libmagic file signatures when C dependency is acceptable
    - Magika sidecar or worker when ML-based type classification is desired
    - go-enry for shebang and programming language hints
checks:
  - declared content type versus detected content type
  - file extension versus detected type
  - executable magic signatures
  - shebang such as sh, bash, python, node, perl, ruby
  - script-like text uploaded as media
  - polyglot or ambiguous signatures
  - archive metadata for policy:archive-bomb-protection
constraints:
  - read bounded prefix on request path
  - never consume stream bytes without rule:prefix-replay
  - use deeper probes only under bounded worker limits
implementation:
  selected_library: decision:mime-detector-library
  upload_path:
    - inspect []byte prefix
    - normalize detected MIME
    - compare against declared content type
    - return data:security-check-result
references:
  - policy:file-intake-security
  - requirement:mime-magic-consistency
  - decision:mime-detector-library
  - rule:prefix-replay
  - policy:archive-bomb-protection
```
