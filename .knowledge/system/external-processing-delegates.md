---
id: system:external-processing-delegates
type: system
title: External Processing Delegates
---
External processing delegates are operator-provided local command adapters for text extraction, OCR, document AI, media, translation, metadata, and custom tasks.

```yaml
adapter_model:
  - each processor declares capability, input match, timing, output merge path, limits, and failure behavior
  - selection uses policy:external-delegation-policy and policy:tool-backend-selection-policy
  - processor output is normalized data:processor-result JSON
  - provider-specific output is normalized by command before stdout
  - provider job id, command version, region, and confidence are recorded when available
local_library_candidates:
  go_docx_ooxml_parser:
    priority: preferred
    capabilities:
      - OOXML text extraction
      - OOXML core properties extraction
    notes:
      - primary path for docx and Office metadata when format is supported
  go_pdf_text_parser:
    priority: preferred_when_quality_ok
    capabilities:
      - simple PDF embedded text extraction
    notes:
      - use only when quality is acceptable; fallback to pdftotext or Tika
  go_exif_parser:
    priority: preferred
    capabilities:
      - selected EXIF metadata extraction
    notes:
      - primary path for allowlisted image metadata without invoking exiftool
local_cli_candidates:
  pdftotext:
    priority: fallback_for_pdf_quality
    capabilities:
      - PDF embedded text extraction
  exiftool:
    priority: fallback_for_broad_metadata
    capabilities:
      - image, media, PDF, and Office metadata extraction
  tesseract:
    capabilities:
      - local OCR
      - language-pack dependent OCR
  ocrmypdf_cli:
    capabilities:
      - searchable PDF generation
      - PDF OCR pipeline
  pandoc:
    capabilities:
      - document text and markup conversion
    notes:
      - optional for text/markdown/html/docx conversion paths
  catdoc_or_antiword:
    capabilities:
      - legacy .doc text extraction
    notes:
      - optional legacy Office fallback when policy allows
self_hosted_sidecar_candidates:
  tika_server:
    capabilities:
      - broad text extraction
      - metadata extraction
    notes:
      - useful as sidecar service for Office, PDF, HTML, and many document formats
  gotenberg:
    capabilities:
      - Office to PDF
      - HTML to PDF
    notes:
      - useful when separating LibreOffice/browser conversion from Go worker
  ocrmypdf:
    capabilities:
      - searchable PDF generation
      - Tesseract OCR pipeline
    notes:
      - useful for PDF OCR normalization
  tika_plus_tesseract:
    capabilities:
      - local text extraction
      - local OCR fallback
  unstructured:
    capabilities:
      - local document partitioning
      - PDF, Office, HTML, and image document parsing
    notes:
      - heavier Python stack; useful for search/RAG pipelines when accepted
external_api_examples_via_local_command:
  aws_textract_with_aws_cli:
    capabilities:
      - OCR
      - handwriting
      - forms
      - tables
      - document layout
    note: implemented by configured local command, not native webhook
  google_document_ai_or_vision:
    capabilities:
      - OCR
      - layout parsing
      - custom extraction
      - document classification and splitting
      - image labels
    note: implemented by gcloud, curl, or custom command
  translation_api:
    capabilities:
      - text translation
      - language detection
    note: command normalizes translated_text into data:processor-result metadata
openai_compatible_api_candidates:
  generic_openai_compatible:
    capabilities:
      - OCR from image or rendered document page
      - image analysis
      - extracted text summary
      - document classification
      - custom JSON extraction
    configuration:
      - prompt templates
      - base URL and model
      - static headers and environment-variable headers
      - response JSON schema
    outputs:
      - data:processor-result
      - data:extracted-content when configured
    note: configured by system:openai-compatible-api-processor
  google_vision_ocr:
    capabilities:
      - image OCR
      - simple OCR fallback
  azure_document_intelligence:
    capabilities:
      - OCR
      - layout
      - tables
      - key value pairs
      - custom and prebuilt document extraction
  abbyy_vantage_or_finereader_engine:
    capabilities:
      - commercial OCR
      - structured document capture
      - high fidelity enterprise OCR
  nanonets_or_docparser:
    capabilities:
      - document extraction SaaS
      - invoice and form oriented extraction
media_candidates:
  local_external_tools:
    priority: preferred_for_image_video_transforms
    capabilities:
      - SIMD/native codec backed image resize and conversion
      - video probe and transcode
    examples:
      - ffmpeg
      - libvips
      - ImageMagick
      - rsvg-convert
  cloudinary:
    capabilities:
      - image and video transformation
      - thumbnail generation
      - CDN delivery
  aws_mediaconvert:
    capabilities:
      - video transcode
      - HLS packaging
  mux:
    capabilities:
      - video ingest
      - HLS playback assets
      - thumbnails
constraints:
  - disabled by default for private content
  - require explicit tenant/product policy to send file bytes outside service boundary
  - prefer native or local command processors before cloud SaaS for private content
  - prefer same cloud account and region when possible
  - support processor timeout, retry, quota, and circuit breaker
  - normalize provider-specific results before stdout and index handoff
references:
  - policy:external-delegation-policy
  - system:local-command-processor
  - system:openai-compatible-api-processor
  - data:processor-result
  - data:openai-compatible-processor-config
  - policy:processor-execution-policy
  - policy:search-extraction-policy
  - system:text-extractor
  - system:ocr-engine
  - system:media-converter
```
