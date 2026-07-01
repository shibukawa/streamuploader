---
id: decision:mime-detector-library
type: decision
title: MIME Detector Library
---
Use github.com/gabriel-vasile/mimetype as the first implementation library for magic-header MIME detection.

```yaml
decision:
  selected: github.com/gabriel-vasile/mimetype
  rationale:
    - pure Go library fits current Go service deployment
    - Detect accepts []byte so upload path can inspect only bounded prefix
    - broad magic signature coverage beyond net/http DetectContentType
    - no libmagic C runtime dependency in container image
  implementation:
    package: github.com/gabriel-vasile/mimetype
    call: mimetype.Detect(prefixBytes).String()
    prefix_limit_bytes: data:security-policy-config mime_magic.prefix_bytes default 3072
    fallback: net/http DetectContentType(prefixBytes)
    normalize_declared: mime.ParseMediaType then strings.ToLower
    replay: io.MultiReader(bytes.NewReader(prefixBytes), remainingBody)
  rejected_alternatives:
    libmagic:
      reason: strongest database but adds C dependency and image/runtime complexity
    github.com/h2non/filetype:
      reason: fast signatures but narrower MIME taxonomy for mismatch policy
    net/http DetectContentType_only:
      reason: standard library only but limited and generic for many file families
    magika:
      reason: useful deeper classifier but too heavy for synchronous upload gate
  risks:
    - some text, office, and container formats can be ambiguous from prefix only
    - archive and polyglot handling needs separate policy:archive-bomb-protection
    - equivalence mapping must avoid allowing executable masquerade
references:
  - requirement:mime-magic-consistency
  - system:content-detector
  - rule:prefix-replay
  - policy:file-intake-security
```
