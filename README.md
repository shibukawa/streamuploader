# Stream Uploader

Service-mediated file upload middleware with a demo download site.

Implemented scope:

- `POST /api/upload/keys`
- `PUT /api/upload/keys/{upload_key}/content`
- `GET /api/upload/keys/{upload_key}`
- `POST /api/upload/wait`
- `GET /api/upload/watch` WebSocket
- `GET /api/file/{object_key}/content` frontend file proxy inline content
- `GET /api/file/{object_key}/download` frontend file proxy attachment download
- `GET /api/file/shared/{shared_key}/content` shared-key inline content
- `GET /api/file/shared/{shared_key}/download` shared-key attachment download
- `GET /api/files/{object_key},{object_key}` multi-file zip download
- backend control `POST /internal/file/presigned-url`
- backend control `POST /internal/file/shared-keys`
- backend control `DELETE /internal/file/shared-keys/{shared_key}`
- backend control `DELETE /internal/objects/{object_key}` on the backend listener
- reverse proxy from non-upload paths to the demo app
- demo app download site with JSON-backed file list, direct/presigned/proxy/shared-key download buttons, and selected-file zip download
- built-in file intake security for MIME/magic-header consistency, script rejection, YAML-managed allow/deny lists, archive bomb protection, and optional ClamAV scanning
- local Kubernetes manifest with streamuploader, demo app, and RustFS

Out of scope for this first implementation: thumbnails, preview generation, resumable upload state, and cleanup worker.

Download modes:

- Direct: the demo app builds a public RustFS/S3 URL. Streamuploader is not involved after upload metadata is stored.
- Presigned: the demo app calls streamuploader backend control `POST /internal/file/presigned-url`, then redirects to the returned S3 URL.
- Proxy: inline content is `GET /api/file/{object_key}/content`; attachment download is `GET /api/file/{object_key}/download`; enable with `SU_ALLOW_FRONTEND_FILE_ACCESS=true`.
- Shared key: the demo app calls `POST /internal/file/shared-keys`, then redirects to `GET /api/file/shared/{shared_key}/download`; enable with `SU_ENABLE_SHARED_KEY=true` and `SU_ALLOW_FRONTEND_FILE_ACCESS=true`.
  One object can have multiple shared keys. Each shared key writes both `.streamuploader/shared/{shared_key}` and `{object_dir}/.shared/{shared_key}` control objects, so `DELETE /internal/objects/{object_key}` can delete the target object and its shared keys together. Individual shares can be deleted with `DELETE /internal/file/shared-keys/{shared_key}`.
- ZIP: selected demo files redirect to `GET /api/files/{object_key},{object_key}`; governed by `SU_MAX_ARCHIVE_FILES` and `SU_MAX_ARCHIVE_BYTES`.

Security configuration:

- Runtime environment variables use the `SU_` prefix. Existing unprefixed names are still read as compatibility fallbacks.
- `SU_SECURITY_CONFIG` points to a YAML policy file. See `config/security.yaml`.
- MIME/magic-header checking is always enabled and cannot be disabled by configuration.
- Use `mime_magic.allow_file_types` as bool switches such as `images: true`, `png: true`, `jpeg: true`, and `pdf: true`; use `allow_mime_types` with exact MIME keys such as `application/pdf: true`.
- Browsers and operating systems do not reliably set script MIME types for selected files, so script-like uploads are detected from shebangs and known script extensions. Use `allowed_script_types` or `allowed_script_extensions` to opt in.
- `file_sanitization` is on by default. JPEG/PNG metadata is stripped without re-encoding, SVG and markup active/external content is rejected, Office/PDF active content is scanned before publish, and legacy `.doc/.xls/.ppt` plus RTF files are rejected unless a per-type `accept_as_is` override is configured.
- `resource_limits` and `structural_validation` enforce parser limits and basic format validity before files are published.
- `SU_MAX_UPLOAD_KEYS_PER_OWNER` limits active `key_created` or `uploading` keys per owner cookie to prevent state exhaustion.
- The YAML security config is validated against the built-in JSON Schema at startup, so unknown file type or MIME keys fail fast. The editor-facing schema is `config/security.schema.json`.
- ClamAV scanning is optional and enabled by setting `SU_CLAMAV_HOST` to a clamd TCP address such as `clamav:3310`; when enabled, uploads are streamed to ClamAV and S3 in parallel and only published after the scan passes.
- If the demo app is opened directly instead of through streamuploader, it proxies `/api/upload/*` to `SU_STREAMUPLOADER_PROXY_URL`.
- The demo includes an `Upload Invalid Files` tab that sends known-bad uploads and shows the JSON rejection response.

## Local build

```bash
go test ./...
go build ./cmd/streamuploader
go build ./demo/app
```

## Docker Compose

```bash
docker compose up --build
curl http://localhost:8080/healthz
curl http://localhost:8082/healthz
```

Open `http://localhost:8080/` for the demo app through streamuploader. This starts `streamuploader` public upload/proxy traffic on `localhost:8080`, backend control on `localhost:8082`, the demo app on `localhost:8081`, and RustFS on `localhost:9000`.

## Tools Image Compose

Use this when you want streamuploader to run from the tools image that already contains the extra conversion tools. Build the tools image first, then start the alternate compose file:

```bash
./scripts/build-tools-image.sh streamuploader:tools
docker compose -f compose.tools.yaml up --build
```

Build the smaller tools image without LibreOffice when Office-to-PDF conversion is not needed:

```bash
./scripts/build-tools-nooffice-image.sh streamuploader:tools-nooffice
docker compose -f compose.tools.nooffice.yaml up --build
```

Set `STREAMUPLOADER_TOOLS_IMAGE` to use a different prebuilt tools image tag:

```bash
STREAMUPLOADER_TOOLS_IMAGE=streamuploader:tools docker compose -f compose.tools.yaml up --build
```

Set `STREAMUPLOADER_TOOLS_NOOFFICE_IMAGE` to use a different prebuilt nooffice tools image tag:

```bash
STREAMUPLOADER_TOOLS_NOOFFICE_IMAGE=streamuploader:tools-nooffice docker compose -f compose.tools.nooffice.yaml up --build
```

This starts the same local stack as `compose.yaml`: streamuploader on `localhost:8080`, backend control on `localhost:8082`, the demo app on `localhost:8081`, RustFS on `localhost:9000`, and ClamAV on `localhost:3310`.

## Host-Native Run

These scripts compile the Go binaries on the host, run streamuploader and the demo app as local processes, and run RustFS in a container. They write binaries, logs, and local data under `.cache/native/`.

Without ClamAV:

```bash
./scripts/run-host-native.sh
```

This uses `config/security.host-native.yaml`, where ClamAV is disabled.

With ClamAV:

```bash
./scripts/run-host-native-clamav.sh
```

This starts an additional ClamAV container on `127.0.0.1:3310` and uses `config/security.host-native-clamav.yaml`, where ClamAV is enabled. The first ClamAV startup can take a while while its database initializes.

Both scripts expose:

- demo through streamuploader: `http://localhost:8080/`
- streamuploader backend control: `http://localhost:8082`
- demo app directly: `http://localhost:8081`
- RustFS API: `http://localhost:9000`
- RustFS console: `http://localhost:9001`

Press `Ctrl+C` to stop. Set `KEEP_CONTAINERS=1` to keep helper containers running after the script exits, and set `CONTAINER_RUNTIME=podman` to use Podman instead of Docker.

## Kubernetes

Build images into your local cluster runtime, then apply:

```bash
kubectl apply -f k8s/local-basic.yaml
kubectl -n streamuploader port-forward svc/streamuploader 8080:8080
kubectl -n streamuploader port-forward svc/rustfs 9000:9000
```

The manifest expects images named `streamuploader:local` and `streamuploader-demo-app:local`.
