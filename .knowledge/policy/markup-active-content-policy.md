---
id: policy:markup-active-content-policy
type: policy
title: Markup Active Content Policy
---
Markup active content policy rejects Markdown, HTML, and XML uploads containing executable, embedded, external, or entity-expansion content.

```yaml
rules:
  scope:
    markdown:
      - text/markdown
      - .md
      - .markdown
    html:
      - text/html
      - application/xhtml+xml
      - .html
      - .htm
      - .xhtml
    xml:
      - application/xml
      - text/xml
      - application/*+xml
      - .xml
  default: reject_active_or_external_content
  inspection:
    markdown:
      - parse Markdown with HTML block and inline HTML visibility
      - inspect raw HTML emitted or preserved by parser
      - reject dangerous HTML elements and attributes before preview, extraction, or durable acceptance
    html:
      - parse with non-executing HTML parser
      - reject active, embedded, frame, form, and external-load features
      - reject event handler attributes and JavaScript URL schemes
    xml:
      - parse with external entity resolution disabled
      - reject DOCTYPE declarations that define external entities
      - reject external parsed entities, external parameter entities, and XInclude
      - enforce XML depth, object count, and parser time limits from policy:resource-limit-policy
  reject_elements:
    html:
      - script
      - iframe
      - object
      - embed
      - applet
      - frame
      - frameset
      - form
      - meta refresh
      - link stylesheet or preload with external URL
    markdown_html:
      - script
      - iframe
      - object
      - embed
      - applet
  reject_attributes:
    - event handler attributes such as onclick and onload
    - src with external URL on active or embedded elements
    - href with javascript URL
    - xlink:href with javascript URL
    - style attributes containing external url reference
  reject_xml_features:
    - external general entity
    - external parameter entity
    - external DTD subset
    - XInclude
    - entity expansion exceeding configured limits
  optional_modes:
    accept_as_is: no inspection
    sanitize_when_supported:
      - remove dangerous HTML elements and attributes
      - reject when sanitizer cannot prove output is safe and structurally valid
  execution:
    - prefix inspection may reject obvious script or iframe tokens
    - full text parse required before final commit when file type is Markdown, HTML, or XML and default mode applies
    - fail closed on malformed, ambiguous, encoding-unsupported, or parser-limit-exceeded documents
  result:
    reject_http_status: 415
    reject_error_code: markup_active_content_rejected
    reason_codes:
      - markup_script_detected
      - markup_iframe_detected
      - markup_external_reference_detected
      - markup_event_handler_detected
      - markup_javascript_url_detected
      - xml_external_entity_detected
      - xml_doctype_external_subset_detected
      - xml_xinclude_detected
      - xml_entity_expansion_limit_exceeded
      - markup_parser_limit_exceeded
references:
  - policy:file-type-sanitization-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - data:security-check-result
```
