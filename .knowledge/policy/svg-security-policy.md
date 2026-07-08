---
id: policy:svg-security-policy
type: policy
title: SVG Security Policy
---
SVG security policy treats SVG as active XML and rejects active or externally loaded content by default.

```yaml
rules:
  scope:
    - image/svg+xml
    - .svg
  default: reject_active_or_external_content
  parser:
    preferred: SAX or streaming XML parser with policy:resource-limit-policy counters
    fallback: full scan before final storage commit
    limits:
      - max_file_size_bytes
      - max_image_width from root width or viewBox width
      - max_image_height from root height or viewBox height
      - max_image_pixel_count from root dimensions or viewBox dimensions
      - max_xml_depth
      - max_object_count
      - max_parser_time_ms
  reject_elements:
    - script
    - foreignObject
    - iframe
    - object
    - embed
    - audio
    - video when external source
    - image when external source
  reject_attributes:
    - href with external URL
    - xlink:href with external URL
    - event handler attributes such as onclick and onload
    - style attributes containing external url reference
  reject_content:
    - external stylesheets
    - external fonts
    - external references
    - JavaScript URLs
    - data URLs unless explicitly allowed by config and bounded size
    - executable or externally loaded content not otherwise named
  optional_modes:
    accept_as_is: no inspection
    sanitize_when_supported:
      - remove dangerous elements and attributes
      - reject when sanitizer cannot preserve well-formed safe SVG
  result:
    reject_http_status: 415
    reject_error_code: svg_active_content_rejected
    reason_codes:
      - svg_script_detected
      - svg_foreign_object_detected
      - svg_external_reference_detected
      - svg_event_handler_detected
      - svg_external_stylesheet_detected
      - svg_external_font_detected
      - svg_parser_limit_exceeded
references:
  - policy:file-type-sanitization-policy
  - policy:resource-limit-policy
  - policy:structural-validation-policy
  - flow:svg-preview-generation
  - data:security-check-result
```
