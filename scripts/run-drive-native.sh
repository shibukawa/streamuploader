#!/usr/bin/env bash
# Runs the Drive on the host against a RustFS container, the same way
# scripts/run-host-native.sh runs streamuploader. Data persists under
# .cache/native/rustfs; the search index under .cache/drive/index-<mode>.
#
# Arguments are passed to the drive binary, so the split deployment can be
# tried with two terminals sharing one RustFS container:
#
#   ./scripts/run-drive-native.sh indexer     # folds and publishes search/{tenant}/
#   ./scripts/run-drive-native.sh server      # follows the snapshot, read-only index
#
# Without arguments it runs all-in-one (server plus indexer in one process).
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME="${CONTAINER_RUNTIME:-docker}"
RUSTFS_NAME="${RUSTFS_CONTAINER_NAME:-streamuploader-rustfs-native}"
RUSTFS_IMAGE="${RUSTFS_IMAGE:-rustfs/rustfs:latest}"
RUSTFS_DATA_DIR="${RUSTFS_DATA_DIR:-$ROOT_DIR/.cache/native/rustfs}"
BIN_DIR="$ROOT_DIR/.cache/drive/bin"
SIDECAR="${DRIVE_SEARCH_BIN:-$ROOT_DIR/search/target/release/drivesearch}"
MODE="${1:-serve}"
PORT="${DRIVE_PORT:-8080}"
INDEX_DIR="${DRIVE_INDEX_DIR:-$ROOT_DIR/.cache/drive/index-$MODE}"
STARTED_RUSTFS=0

cleanup() {
  if [[ "$STARTED_RUSTFS" == "1" && "${KEEP_CONTAINERS:-0}" != "1" ]]; then
    "$RUNTIME" stop "$RUSTFS_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

mkdir -p "$BIN_DIR" "$RUSTFS_DATA_DIR"
if [[ ! -x "$SIDECAR" ]]; then
  echo "==> building search sidecar"
  (cd "$ROOT_DIR/search" && PATH="$HOME/.cargo/bin:$PATH" cargo build --release)
fi
echo "==> building drive"
(cd "$ROOT_DIR" && go tool pw generate)
(cd "$ROOT_DIR" && go build -o "$BIN_DIR/drive" ./cmd/drive)

if "$RUNTIME" ps --format '{{.Names}}' | grep -qx "$RUSTFS_NAME"; then
  echo "==> reusing RustFS container $RUSTFS_NAME"
else
  echo "==> starting RustFS container $RUSTFS_NAME"
  "$RUNTIME" rm -f "$RUSTFS_NAME" >/dev/null 2>&1 || true
  "$RUNTIME" run -d --name "$RUSTFS_NAME" \
    -e RUSTFS_ACCESS_KEY=rustfsadmin -e RUSTFS_SECRET_KEY=rustfsadmin -e RUSTFS_CONSOLE_ENABLE=true \
    -p 9000:9000 -p 9001:9001 -v "$RUSTFS_DATA_DIR:/data" "$RUSTFS_IMAGE" /data >/dev/null
  STARTED_RUSTFS=1
fi
for _ in {1..120}; do
  (echo >"/dev/tcp/127.0.0.1/9000") >/dev/null 2>&1 && break
  sleep 1
done

if [[ "$MODE" == "indexer" ]]; then
  echo "==> drive indexer (RustFS at http://localhost:9000, index dir $INDEX_DIR)"
else
  echo "==> drive $MODE on http://localhost:$PORT (RustFS at http://localhost:9000, index dir $INDEX_DIR)"
fi
exec env \
  DRIVE_SEARCH_BIN="$SIDECAR" \
  DRIVE_INDEX_DIR="$INDEX_DIR" \
  DRIVE_DELIVERY="${DRIVE_DELIVERY:-proxy}" \
  PORT="$PORT" \
  SU_PUBLIC_BASE_URL="http://localhost:$PORT" \
  SU_S3_BUCKET="${SU_S3_BUCKET:-stream-upload}" \
  SU_S3_ENDPOINT="http://127.0.0.1:9000" \
  SU_S3_PUBLIC_ENDPOINT="http://localhost:9000" \
  SU_S3_REGION=us-east-1 \
  SU_S3_FORCE_PATH_STYLE=true \
  SU_S3_ACCESS_KEY_ID=rustfsadmin \
  SU_S3_SECRET_ACCESS_KEY=rustfsadmin \
  SU_THUMBNAILS_ENABLED=true \
  SU_SECURITY_CONFIG="${SU_SECURITY_CONFIG:-$ROOT_DIR/config/security.host-native.yaml}" \
  "$BIN_DIR/drive" "$@"
