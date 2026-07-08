---
id: policy:structural-validation-policy
type: policy
title: Structural Validation Policy
---
Structural validation policy rejects malformed or inconsistent files before sanitization or active-content inspection.

```yaml
rules:
  default: validate_when_supported
  config_source: data:security-policy-config structural_validation
  validators:
    png:
      - chunk order
      - chunk length bounds
      - CRC validation
      - critical chunk presence
    jpeg:
      - marker sequence
      - segment length bounds
      - single coherent image stream
    zip:
      - central directory consistency
      - local header consistency
      - ZIP64 consistency
      - path safety
	    office_open_xml:
	      - valid ZIP package
	      - required [Content_Types].xml part
	      - declared document family matches an Override main part content type
	      - Override main PartName exists as an actual package part
	      - relationship target consistency
	      - no ambiguous duplicate package parts
	    open_document:
	      - valid ZIP package
	      - required mimetype part matches declared odt, ods, or odp family
	      - required META-INF/manifest.xml root file-entry media type matches declared family
	      - required content.xml part exists
	      - XML parts are parser-readable under configured decompressed size limits
    pdf:
      - cross-reference consistency
      - object stream consistency
      - trailer and catalog consistency
      - stream length consistency within parser limits
    svg:
      - well-formed XML
      - parser limit compliance
    html:
      - parser tree construction completes within limits
      - encoding declaration is coherent
    xml:
      - well-formed XML
      - parser limit compliance
      - no malformed entity declarations
  reject:
    - malformed structure
    - inconsistent directory or object metadata
    - truncated required data
    - duplicate or ambiguous records that affect interpretation
    - parser reaches configured resource limit before validation completes
  behavior:
    - fail closed in strict mode
    - use bounded parse and resource counters from policy:resource-limit-policy
    - validation must run before sanitize writers rewrite bytes
    - chunk-only validation may run before storage upload when bounded reads prove result
    - full-file validation runs before final commit for formats needing random access or complete graph checks
  result:
    reject_http_status: 415
    reject_error_code: structural_validation_failed
references:
  - data:security-policy-config
  - policy:resource-limit-policy
  - policy:file-type-sanitization-policy
  - data:security-check-result
```
