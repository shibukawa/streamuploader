---
id: policy:auth-extension-license-exception
type: policy
title: Auth Extension License Exception
---
Auth extension license exception allows deployment-specific authentication middleware to remain outside the AGPL obligation for streamuploader core.

```yaml
intent:
  - streamuploader core is AGPL
  - authentication customization surface is a license exception boundary
  - deployments may keep proprietary or private auth integration code separate
covered_extension_points:
  - streamuploader/auth public package
  - api:auth-middleware-extension-api frontend function
  - api:auth-middleware-extension-api backend function
  - data:auth-context values passed through request context
allowed_private_code:
  - identity provider adapters
  - token validators and JWKS clients
  - enterprise SSO and gateway header verification
  - tenant, role, scope, and permission mapping
  - secret loading for auth provider credentials
boundary_rules:
  - exception applies only to code needed to implement frontend or backend auth middleware
  - modified streamuploader core outside the auth extension boundary remains under project license
  - auth middleware may call unmodified exported core helper types when those helpers are part of the extension contract
  - copying unrelated core logic into proprietary auth code is outside exception intent
  - license notice should identify the exact files or packages covered by the exception
repository_layout_options:
  in_tree_exception_package:
    description: package contains default pass-through or token auth and documented replacement points
  separate_module:
    description: deployment builds with private module that provides middleware factories
  build_tag_overlay:
    description: private files selected by build tags replace default auth middleware functions
documentation_required:
  - AGPL license for streamuploader core
  - additional permission text for auth extension boundary
  - list of covered function signatures and package paths
  - statement that no warranty or security guarantee is implied by custom auth
references:
  - api:auth-middleware-extension-api
  - policy:frontend-auth-extension-policy
  - policy:backend-auth-extension-policy
```
