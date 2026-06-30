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
- local Kubernetes manifest with streamuploader, demo app, and RustFS

Out of scope for this first implementation: thumbnails, preview generation, virus scanning, resumable upload state, and cleanup worker.

Download modes:

- Direct: the demo app builds a public RustFS/S3 URL. Streamuploader is not involved after upload metadata is stored.
- Presigned: the demo app calls streamuploader backend control `POST /internal/file/presigned-url`, then redirects to the returned S3 URL.
- Proxy: inline content is `GET /api/file/{object_key}/content`; attachment download is `GET /api/file/{object_key}/download`; enable with `ALLOW_FRONTEND_FILE_ACCESS=true`.
- Shared key: the demo app calls `POST /internal/file/shared-keys`, then redirects to `GET /api/file/shared/{shared_key}/download`; enable with `ENABLE_SHARED_KEY=true` and `ALLOW_FRONTEND_FILE_ACCESS=true`.
  One object can have multiple shared keys. Each shared key writes both `.streamuploader/shared/{shared_key}` and `{object_dir}/.shared/{shared_key}` control objects, so `DELETE /internal/objects/{object_key}` can delete the target object and its shared keys together. Individual shares can be deleted with `DELETE /internal/file/shared-keys/{shared_key}`.
- ZIP: selected demo files redirect to `GET /api/files/{object_key},{object_key}`; governed by `MAX_ARCHIVE_FILES` and `MAX_ARCHIVE_BYTES`.

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

## Kubernetes

Build images into your local cluster runtime, then apply:

```bash
kubectl apply -f k8s/local-basic.yaml
kubectl -n streamuploader port-forward svc/streamuploader 8080:8080
kubectl -n streamuploader port-forward svc/rustfs 9000:9000
```

The manifest expects images named `streamuploader:local` and `streamuploader-demo-app:local`.
