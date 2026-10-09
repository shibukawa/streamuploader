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
- `SU_OBJECT_LOCK_MODE` (`governance`, `compliance` or `legal_hold`) with `SU_OBJECT_LOCK_RETENTION` puts S3 Object Lock headers on every final uploaded object; temporary, derived and sentinel objects stay unlocked. `SU_WORM_MODE=true` makes `DELETE /internal/objects/{object_key}` and `DELETE /internal/file/shared-keys/{shared_key}` answer `405 worm_readonly`. Both are used by the Drive's WORM audit mode (see below) and are off by default.
- The YAML security config is validated against the built-in JSON Schema at startup, so unknown file type or MIME keys fail fast. The editor-facing schema is `config/security.schema.json`.
- ClamAV scanning is optional and enabled by setting `SU_CLAMAV_HOST` to a clamd TCP address such as `clamav:3310`; when enabled, uploads are streamed to ClamAV and S3 in parallel and only published after the scan passes.
- If the demo app is opened directly instead of through streamuploader, it proxies `/api/upload/*` to `SU_STREAMUPLOADER_PROXY_URL`.
- The demo includes an `Upload Invalid Files` tab that sends known-bad uploads and shows the JSON rejection response.

Authentication extension:

- Streamuploader core does not implement built-in application authentication for frontend or backend routes. The default frontend and backend auth middleware are pass-through.
- Production deployments may protect backend control routes with external controls such as security groups, firewall rules, API Gateway, ingress policy, service mesh policy, or private listener placement.
- Deployments that need application-level authentication can replace the middleware through the public `streamuploader/auth` package. `SetFrontendAuthMiddleware` wraps browser-facing upload and file APIs. `SetBackendAuthMiddleware` wraps backend control APIs.
- The old built-in `BACKEND_AUTH_TOKEN` behavior has been removed from core. Streamuploader provides only hook points; any bearer-token, gateway-header, SSO, or other auth behavior belongs in a custom main or deployment-owned package.

Current-equivalent backend bearer token sample:

```go
package main

import (
	"net/http"
	"os"

	"streamuploader/auth"
	"streamuploader/streamuploadercli"
)

func main() {
	token := os.Getenv("SU_BACKEND_AUTH_TOKEN")
	auth.SetBackendAuthMiddleware(func(next http.Handler, _ *auth.Config) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	streamuploadercli.Main()
}
```

Build that custom main instead of `./cmd/streamuploader`, set `SU_BACKEND_AUTH_TOKEN`, and backend control requests must send:

```http
Authorization: Bearer <token>
```

Custom middleware can be installed directly from `main`:

```go
package main

import (
	"net/http"

	"streamuploader/auth"
	"streamuploader/streamuploadercli"
)

func main() {
	auth.SetBackendAuthMiddleware(func(next http.Handler, _ *auth.Config) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Gateway-Actor") == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	streamuploadercli.Main()
}
```

License:

- Streamuploader core is licensed under the GNU AGPLv3. See `LICENSE`.
- Authentication middleware extension code that uses only the public `streamuploader/auth` boundary may be distributed under other terms under the additional permission in `LICENSE-AUTH-EXCEPTION`.
- Changes outside that authentication middleware boundary remain governed by the AGPLv3.

## PDF and Office documents

PDF, OOXML Office files (`.docx`, `.xlsx`, `.pptx`), Photoshop (`.psd`, `.psb`), Illustrator (`.ai`, PDF-compatible only), EPUB, Parquet, Visio (`.vsdx`), CSV/TSV, Markdown, HTML and draw.io (`.drawio`) files are processed in-process with [bdf](https://github.com/shibukawa/bdf); no office suite or external tool is involved. For every accepted upload streamuploader stores:

- a thumbnail of the first page (`<object key>/thumbnail`, re-encoded through the thumbnail format policy),
- the search text as extracted content (`<object key>.text.json`): the joined text under `texts.extracted` and the per-page text under `pages` (`view`, `page` from 1, `text`; select it with `include=pages`), so a hit can link to its page,
- the whole document as a single-file BDF preview (`<object key>.bdf`, `application/x-bdf`, served at `/api/file/{key}/preview`).

These three are built in and have no on/off switch; `thumbnails.enabled` and `text_extraction.enabled` only affect other file types. To refuse a type, use `mime_magic.deny_file_types`. Legacy `.doc`/`.xls`/`.ppt` and OpenDocument files are rejected. Photoshop files cannot be metadata-sanitized, so like TIFF/AVIF/HEIC they need a per-file-type `accept_as_is` mode under the default image policy. Illustrator files saved without PDF content (and Illustrator 8 or older) cannot be converted. PSD/AI have no search text.

Formats are checked by content, not only by name or declared type. Binary formats must be confirmed by bdf's detection plus a structure check (a Parquet footer, an EPUB with its package document, a Visio package); otherwise the upload is refused with `415 document_format_mismatch`, and obviously wrong first bytes are refused before the rest of the file is read. Text formats are nominated by extension, declared type or content (a `text/plain` upload that is an HTML page or a draw.io diagram is recognized) and confirmed by parsing (a `.csv` must parse as a table with consistent columns); a text file that is not what its name suggests is not refused but handled as plain text, as before. CSV/Markdown/HTML/draw.io keep the text extractor's `texts.text` (and its external command and metadata options) next to the bdf `pages`. Conversion never fetches images from the network (HTML, Markdown and EPUB can reference remote images; they are left out).

Password-protected documents: the first upload returns `422 document_password_required`. Send the file again with the password in the `X-Document-Password` header; a wrong password returns `422 document_password_invalid` and nothing is stored. An accepted protected document shows a lock mark (`thumbnail.status` and `extracted_content.status` are `locked`, `protected` is `true`), no search text is extracted, and its `.bdf` is sealed with the same password. A protected Office file is recognized from its first bytes, so the password request is returned before the rest of the upload is read; a protected PDF is only recognized after the whole file arrived. The password is never stored or logged. For a PDF the thumbnail is stored as soon as the first page is converted, before the remaining pages and the `.bdf` are finished.

`document_processing` in the security config tunes the behavior: `fail_upload_on_error` (default `false`) cancels the upload with `422 document_processing_failed` when a document cannot be converted, instead of accepting it with failed derived assets (env `DOCUMENT_PROCESSING_FAIL_UPLOAD_ON_ERROR`; it forces sequential execution); `max_input_bytes` bounds the document read for conversion; `execution_mode` is `async` or `sequential`.

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

Set `STREAMUPLOADER_TOOLS_IMAGE` to use a different prebuilt tools image tag:

```bash
STREAMUPLOADER_TOOLS_IMAGE=streamuploader:tools docker compose -f compose.tools.yaml up --build
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

## Drive (all-in-one)

`cmd/drive` turns streamuploader into a self-hosted, viewer-first file service: upload through the streamuploader API, browse files as tag folders, search file names, body text and authors in Japanese, and open documents in the bdf viewer at the matching page. The design is recorded in `.knowledge/concepts/vision/local-drive.yaml` and the concepts it links to.

How it fits together:

- Durable state is only in the bucket. Metadata changes are append-only journal events (`drive/journal/{tenant}/{yyyy}/{mm}/{ulid}.json`); the indexer folds them into `drive/meta/{tenant}/{file_id}.json` snapshots and a local tantivy index; a server patches results with the journal entries the indexer has not folded yet, so edits are visible at once.
- The index itself is published to the bucket as a snapshot under `drive/search/{tenant}/`: the immutable tantivy segment files plus `meta.json`, which is tantivy's own meta.json extended with a `drive` object (generation, the last folded journal key, the file list, retired files and a short change history). Segments are uploaded first and the pointer last. Servers follow the pointer with a read-only sidecar, the indexer resumes from it after a restart, and segment files no pointer references are deleted after a grace period. This is what lets servers and the indexer run as separate processes.
- Search runs in `search/`, a Rust sidecar (`drivesearch`) built on tantivy with the lindera IPADIC tokenizer. The Go server talks to it over JSON lines on stdin/stdout. Tags, type, date and author are hierarchical facets, which is what makes "tags as folders" work.
- The web UI (`drive/ui`) is a plain ES module bundled with esbuild; the built `dist/` is embedded into the binary and uses `@bdfkit/viewer` to render `.bdf` previews.

Run it without any object store (data lives in memory, lost on exit):

```bash
./scripts/run-drive-memory.sh
```

Run it on the host against a RustFS container:

```bash
./scripts/run-drive-native.sh
```

Then open `http://localhost:8090/` (memory) or `http://localhost:8080/` (RustFS). `./scripts/drive-demo-seed.sh http://localhost:8090` uploads three fixtures through the API.

Build from source:

```bash
(cd search && cargo build --release)      # sidecar, Rust 1.85+; ~50 MB with the IPADIC dictionary
(cd drive/ui && npm install && npm run build)   # UI bundle, only after changing drive/ui/src
go build ./cmd/drive
```

`Dockerfile.drive` builds both binaries into one image; `drive` finds `drivesearch` next to itself or through `DRIVE_SEARCH_BIN`.

Subcommands:

| Command | Role |
|---|---|
| `drive` | all-in-one: server plus indexer loop in one process |
| `drive server` | server only: upload intake, Drive API, web UI and search over the published snapshot; the index is read-only and refreshed from the bucket; any number of instances |
| `drive indexer` | indexer only: folds journal events and extracted text, writes meta snapshots, publishes the index snapshot, in a loop; run exactly one per tenant |
| `drive index-once` | one indexer run (fold, publish) and exit; for a cron job or a Cloud Run Job |
| `drive reindex` | rebuild the index from the bucket, publish it and exit |
| `drive verify` | walk the audit checkpoint chain and report tampering (WORM mode); exits non-zero on findings |
| `drive init` | write the deployment files for a target (below) |

At start an indexer adopts the published snapshot when its local directory is missing, stale or behind it, and rebuilds from the meta snapshots only when there is nothing to adopt. A server starts serving as soon as it has downloaded the current snapshot; until the first publish it serves an empty index plus the journal overlay. Edits are visible on every server at once (overlay) and from the index after the next indexer run and refresh. Rebuilds are started where the indexer runs (`drive reindex`); `POST /api/drive/admin/reindex` answers 503 on a server-only process.

Split run on the host (two terminals, one RustFS container):

```bash
./scripts/run-drive-native.sh indexer
```

```bash
./scripts/run-drive-native.sh server
```

### Deployment kit: `drive init`

`drive init` writes everything one deployment needs into `deploy/<target>/` so that each runtime mode is maintained once, as a template, instead of by hand per deployment. The first target is docker compose with a local RustFS bucket:

```bash
go run ./cmd/drive init --target compose
cd deploy/compose && docker compose up --build -d
```

The kit is `.env` (every setting, read by compose and passed to the container), `compose.yaml` (drive built from this checkout with `Dockerfile.drive`, rustfs, optionally clamav), `security.yaml` (a copy of `config/security.yaml`) and a `README.md` with the m1 checks. Nothing needs editing before the first `up`.

| Flag | Default | Meaning |
|---|---|---|
| `--target` | `compose` | `compose`; `aws`, `google`, `azure`, `cloudflare` are planned and refused with the reason (exit 2) |
| `--storage` | per target | `rustfs` for compose; others arrive with the cloud kits |
| `--delivery` | `proxy` | `proxy` or `presigned` (see `DRIVE_DELIVERY`) |
| `--clamav` | off | add a ClamAV service and scan every upload |
| `--image REF` | build locally | run a prebuilt image instead of building from `--context` |
| `--context DIR` | this checkout | build context for `Dockerfile.drive`; found by walking up to `go.mod` |
| `--name` | `drive` | compose project name and the `<name>:local` image tag |
| `--port` | `8080` | host port of the Drive |
| `--out DIR` | `deploy/<target>` | output directory |
| `--force` | off | overwrite existing files (otherwise exit 3, nothing written) |
| `--dry-run` | off | list the files and exit |

Generated files carry no credentials except the development pair of the local RustFS. Re-running `drive init --force` regenerates the kit from the flags, not from edits. The requirement, the per-target limits and the open questions are in `.knowledge/concepts/requirement/drive-init-subcommand.yaml`; the generator is `drive/deploy` with golden kits under `drive/deploy/testdata`.

Environment (in addition to the `SU_*` variables of streamuploader, whose S3 settings the Drive reuses):

| Variable | Default | Meaning |
|---|---|---|
| `DRIVE_TENANT` | `default` | tenant id used in every key and index document |
| `DRIVE_PREFIX` | `drive/` | key prefix of journal, meta and audit objects in the bucket |
| `DRIVE_INDEX_DIR` | `.cache/drive/index` | local tantivy directory; deleting it forces a rebuild |
| `DRIVE_SEARCH_BIN` | next to the binary, then `PATH` | path of `drivesearch` |
| `DRIVE_SEARCH_TOKENIZER` | `lindera` | `lindera` (IPADIC) or `ngram` |
| `DRIVE_INDEX_INTERVAL` | `30s` | indexer period; in all-in-one mode writes also poke the indexer immediately |
| `DRIVE_INDEX_SNAPSHOT` | `true` | publish the index to `drive/search/{tenant}/` after every commit and adopt it at start; `false` keeps the index local (all-in-one only) |
| `DRIVE_SNAPSHOT_REFRESH` | `10s` | server mode: how often the pointer is checked for a new generation (one HEAD request) |
| `DRIVE_SNAPSHOT_GC_GRACE` | `15m` | how long a segment file no pointer references stays in the bucket; keep it longer than the refresh interval plus a download |
| `DRIVE_DELIVERY` | `proxy` | `proxy` streams bytes through the server; `presigned` redirects to the bucket (needs a browser-reachable endpoint with CORS for range requests) |
| `DRIVE_STORAGE` | `s3` | `memory` for a throwaway run |
| `DRIVE_WORM_MODE` | `off` | `append_only` or `strict` turns on the WORM audit mode described below |
| `DRIVE_WORM_ACCESS_WINDOW` | `1m` | repeated reads of one object by one client within this window produce a single `file.accessed` event; `0` records every request |
| `SU_OBJECT_LOCK_MODE` | unset | `governance`, `compliance` or `legal_hold`: the Object Lock put on originals, journal events and checkpoints |
| `SU_OBJECT_LOCK_RETENTION` | unset | retention period for `governance` and `compliance`, such as `87600h` |

API (same origin as the upload API; no authentication yet):

- `GET /api/drive/info` describes the deployment: tenant, delivery mode, WORM mode and the storage lock it relies on
- `POST /api/drive/files` registers an upload: body `{"upload": <item from POST /api/upload/wait>, "tags": ["projects/2026"], "author": "...", "location": {"lat":..,"lon":..}}`
- `GET /api/drive/files/{id}`, `PATCH /api/drive/files/{id}` (`name`, `tags`, `author`, `location`, `clear_location`, `expected_revision`), `DELETE /api/drive/files/{id}`, `POST /api/drive/files/{id}/restore`
- `POST /api/drive/files/{id}/versions` with `{"upload": <item>, "expected_revision": n}` replaces the bytes with a new upload; the earlier object stays and is listed by `GET /api/drive/files/{id}/versions` and served by `GET /api/drive/files/{id}/versions/{n}/content|download`
- `GET /api/drive/files/{id}/content|download|preview|thumbnail`
- `GET /api/drive/search?q=&tag=&facet=&exact=&sort=&limit=&offset=` returns hits with the best page and a highlighted snippet
- `GET /api/drive/facets?path=/tags/projects` lists the children of a facet path with counts (`/tags`, `/type`, `/date`, `/author`, `/geo`)
- `GET /api/drive/journal?since=&until=&type=&limit=` streams journal events as JSON lines (`since`/`until` take a journal key, an event id or an RFC 3339 time)
- `GET /api/drive/checkpoints`, `GET /api/drive/checkpoints/{n}` expose the audit chain (WORM mode)
- `GET /api/drive/stats` (includes `indexer`: mode, snapshot generation, last journal key), `POST /api/drive/admin/reindex` (all-in-one only)

Tests: `go test ./drive/...` includes `drive/e2e`, which runs the whole stack in one process against the in-memory store and needs the built sidecar (`search/target/release/drivesearch` or `DRIVE_SEARCH_BIN`); without it the sidecar tests are skipped. `TestServerIndexerSplit` runs an indexer and two server-only processes against one in-memory bucket.

### WORM audit mode

`DRIVE_WORM_MODE=strict` (or `append_only`) turns the Drive into a write-once-read-many archive: nothing registered can be edited, overwritten or deleted, every read is recorded, and tampering with the bucket is detectable. The design is in `.knowledge/concepts/requirement/local-drive-worm-audit-mode.yaml` and `policy/worm-enforcement.yaml`. Enforcement has three layers:

1. **Application.** `DELETE /api/drive/files/{id}` and `POST .../restore` answer `405 {"code":"worm_readonly"}`. `PATCH` is refused in `strict`; in `append_only` it is accepted only when it adds something (new tags, a first author override, a first location) and refused when it renames, removes a tag, rewrites the author or clears the location. The only way to change bytes is `POST /api/drive/files/{id}/versions`; every earlier version keeps its object and stays downloadable. Streamuploader's backend control routes `DELETE /internal/objects/{key}` and `DELETE /internal/file/shared-keys/{key}` answer `405 worm_readonly` too (`SU_WORM_MODE=true`, set automatically by the Drive).
   Every download, inline view, preview and thumbnail read writes a `file.accessed` journal event with the actor, time, object key, kind and a hash of the client address before any byte is served; if the event cannot be written the read fails. With `DRIVE_DELIVERY=presigned` the URL is issued together with the event and its lifetime is capped at five minutes. A viewer's burst of range requests on one object is collapsed into one event per `DRIVE_WORM_ACCESS_WINDOW`; `HEAD` requests are not reads.
2. **Storage lock.** `SU_OBJECT_LOCK_MODE` plus `SU_OBJECT_LOCK_RETENTION` (or `legal_hold` for an indefinite lock) put S3 Object Lock headers on every original at upload and, in WORM mode, on every journal event and audit checkpoint. Derived assets (`thumbnail`, `.text.json`, `.bdf`), meta snapshots, work sentinels and temporary objects are never locked because they are reproducible or short-lived. The bucket must have Object Lock enabled (AWS S3, Backblaze B2, MinIO) or the writes fail, which is intended: a WORM deployment must not silently run unlocked. Cloudflare R2 locks by bucket rule instead of per-object headers and GCS uses its own retention API, so on those providers leave `SU_OBJECT_LOCK_MODE` unset and configure the bucket; the Drive then logs and reports (`GET /api/drive/info`) that it is "WORM by application and checkpoint only". The credentials the Drive uses should not be allowed to change lock configuration.
3. **Tamper evidence.** After every fold the indexer writes `drive/audit/{tenant}/checkpoint-{n}.json`: the keys and SHA-256 of every journal event folded since the previous checkpoint, a Merkle root over them, the originals registered in that range with their checksums, and the SHA-256 of the previous checkpoint (checkpoint 1 links to a per-tenant genesis value). `drive verify` walks the chain, re-lists the journal and names every missing, extra or modified event and every missing or resized original; `drive verify --hash` also re-reads each original and compares it with the checksum recorded at upload. It exits non-zero on any finding, so it can run on a schedule. A restarted or rebuilt process continues the chain from the bucket; the journal is never compacted in WORM mode, so a full replay stays possible.

```bash
DRIVE_WORM_MODE=strict SU_OBJECT_LOCK_MODE=compliance SU_OBJECT_LOCK_RETENTION=87600h ./drive
./drive verify --hash
```

Switching a tenant from `off` to a WORM mode is safe at any time; the first checkpoint then covers the whole existing journal. Switching from `append_only` to `strict` later is also safe. Switching WORM off again does not remove any lock the storage already holds.
