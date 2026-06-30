---
id: system:text-extractor
type: system
title: Text Extractor
---
Text extractor is an isolated worker that extracts searchable text and metadata from accepted files.

```yaml
selection:
  - prefer Go libraries for EXIF, OOXML, simple PDF text, and text-like formats
  - use external CLI or sidecar only when Go parser coverage or quality is insufficient
  - keep image and video transform heavy paths in external tools first because SIMD/native codecs are usually faster
backends:
  direct_text:
    capabilities:
      - text/plain normalization
      - csv/json/xml/html text extraction
  docx_parser:
    preferred: true
    capabilities:
      - OOXML document text extraction
      - core properties extraction
  go_exif_parser:
    preferred: true
    capabilities:
      - allowlisted EXIF extraction
      - common image metadata extraction
  go_office_metadata_parser:
    preferred: true
    capabilities:
      - OOXML core properties
      - document metadata allowlist
  go_pdf_text_parser:
    preferred: conditional
    capabilities:
      - simple PDF embedded text extraction
    notes:
      - fallback to pdftotext or Tika when layout or encoding quality is insufficient
  pandoc:
    optional: true
    capabilities:
      - document conversion to plain text or markdown
  antiword_or_catdoc:
    optional: true
    capabilities:
      - legacy .doc text extraction
  pdftotext:
    fallback: true
    command: pdftotext -v
    capabilities:
      - pdf text extraction
  tika:
    optional: true
    capabilities:
      - broad document text extraction
      - metadata extraction
      - Office and PDF fallback
  tika_server:
    optional: true
    capabilities:
      - sidecar broad text extraction
      - sidecar metadata extraction
  cloud_document_ai:
    optional: true
    capabilities:
      - delegated document text extraction
      - delegated structured extraction
  libreoffice:
    capabilities:
      - office text export fallback
      - office to PDF for OCR path
  exiftool:
    fallback: true
    command: exiftool -ver
    capabilities:
      - EXIF and file metadata extraction
constraints:
  - run outside request path
  - isolate process and temporary files
  - disable external network access
  - enforce timeout, CPU, memory, input size, page count, and output size
  - normalize output to UTF-8
  - treat extraction failure as derived content failure
references:
  - flow:text-extraction-generation
  - data:extracted-content
  - system:external-tool-registry
  - system:external-processing-delegates
  - policy:external-delegation-policy
  - policy:tool-backend-selection-policy
```
