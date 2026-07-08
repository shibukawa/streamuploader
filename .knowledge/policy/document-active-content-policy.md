---
id: policy:document-active-content-policy
type: policy
title: Document Active Content Policy
---
Document active content policy rejects Office Open XML, OpenDocument, and PDF uploads containing dangerous, executable, externally loaded, or embedded active features.

```yaml
rules:
  scope:
    office_open_xml:
      - docx
      - xlsx
      - pptx
    open_document:
      - odt
      - ods
      - odp
    pdf:
      - pdf
  default: reject_on_detection
	  inspection:
	    office_open_xml:
	      - validate ZIP package and [Content_Types].xml before active content inspection
	      - require declared docx, xlsx, or pptx family to match an existing main package part
	      - inspect relationships for external targets
	    open_document:
	      - validate ZIP package, mimetype, manifest root media type, and content.xml before active content inspection
	      - reject script and Basic macro package parts
	      - inspect XML parts for script event listeners, StarBasic markers, and external xlink targets
      - inspect package parts for macros, OLE, ActiveX, embedded packages, and external references
      - enforce archive limits from policy:archive-bomb-protection and policy:resource-limit-policy
    pdf:
      - validate xref and object graph consistency
      - inspect catalog, name trees, actions, annotations, embedded files, RichMedia, JavaScript, XFA, and 3D entries
      - enforce page, object, stream, and decompressed size limits from policy:resource-limit-policy
  reject_features:
    common:
      - encryption or password protection
      - external links or external references
      - embedded objects
      - embedded or attached files
      - active or executable content not otherwise named
    office_open_xml:
      - VBA macros
      - OLE objects
      - ActiveX controls
      - external workbook links
      - external images or relationships
      - embedded packages
    pdf:
      - JavaScript
      - Launch actions
      - OpenAction
      - AdditionalActions
      - embedded files
      - RichMedia
      - 3D objects
      - XFA forms
  optional_modes:
    accept_as_is: no inspection
    sanitize_when_supported:
      - remove unsupported or dangerous features only when parser and writer preserve valid document structure
      - reject when sanitize cannot prove removal
  execution:
    - full file scan required before final commit
    - scan may run in parallel worker after staging bytes
    - fail closed on encrypted, malformed, ambiguous, or parser-unsupported documents in default mode
  result:
    reject_http_status: 415
    reject_error_code: document_active_content_rejected
    reason_codes:
      - document_encrypted
      - document_macro_detected
      - document_external_reference_detected
      - document_embedded_object_detected
      - document_javascript_detected
      - document_launch_action_detected
      - document_open_action_detected
      - document_rich_media_detected
      - document_xfa_detected
references:
  - policy:file-type-sanitization-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - policy:archive-bomb-protection
  - data:security-check-result
```
