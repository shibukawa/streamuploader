---
id: policy:external-delegation-policy
type: policy
title: External Delegation Policy
---
External delegation policy controls when local command processors may call external providers or move file content outside the deployment boundary.

```yaml
default:
  - streamuploader core does not implement generic provider webhooks
  - OpenAI-compatible APIs may be called by system:openai-compatible-api-processor when explicitly configured
  - external APIs are reached through system:local-command-processor
  - command owns provider-specific auth, request payload, retry, and response normalization
  - do not delegate private file bytes to external SaaS providers unless explicitly allowed
  - local native and local command processors are preferred before remote providers for private content
  - cloud provider use requires explicit opt-in per tenant, file role, and capability
selection_inputs:
  - tenant policy
  - file privacy classification
  - content type
  - requested capability
  - provider region
  - provider cost class
  - provider data retention terms
  - provider accuracy requirement
handoff_modes:
  local_cli:
    description: worker invokes configured argv and receives data:processor-result JSON
    data_boundary: same host or container
  local_cli_calls_provider:
    description: command may call aws, gcloud, curl, or custom clients
    data_boundary: command-defined external boundary
    constraints:
      - command must normalize provider output before stdout
      - streamuploader does not parse provider-specific response shapes
      - secrets are passed by allowlisted env or mounted files
  self_hosted_sidecar_via_command:
    description: command calls a private sidecar or internal service
    data_boundary: deployment private network
  upload_bytes:
    description: command downloads from S3 and sends bytes to provider API
    data_boundary: external provider
  provider_object_reference:
    description: provider reads object using short-lived scoped URL or cloud integration
    constraints:
      - prefer dedicated temporary object or restricted access path
      - avoid exposing canonical capability key
      - set short expiration and audit use
  openai_compatible_api:
    description: worker calls configured OpenAI-compatible endpoint with prompt and JSON schema
    data_boundary: configured provider boundary
    constraints:
      - endpoint must be configured and allowlisted
      - headers may interpolate only allowlisted environment variables
      - response JSON schema validation required before merge
      - original file bytes require explicit privacy opt-in
privacy_controls:
  - field allowlist and denylist for extracted metadata
  - redact or skip documents marked do_not_index
  - disallow delegation for regulated or confidential classifications unless explicitly permitted
  - store provider response only after normalization and retention filtering
operations:
  - record provider name, version, region, request id, job id, and cost estimate
  - use idempotency key per processor job when command supports it
  - enforce timeout, stdout limit, retry budget, quota, and circuit breaker
  - support fallback to local backend or skip when provider fails
references:
  - system:external-processing-delegates
  - system:openai-compatible-api-processor
  - system:local-command-processor
  - policy:processor-execution-policy
  - policy:search-extraction-policy
  - policy:audit-log-policy
  - policy:worker-queue-policy
```
