#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME="${CONTAINER_RUNTIME:-docker}"
RUSTFS_NAME="${RUSTFS_CONTAINER_NAME:-streamuploader-rustfs-native}"
CLAMAV_NAME="${CLAMAV_CONTAINER_NAME:-streamuploader-clamav-native}"
RUSTFS_IMAGE="${RUSTFS_IMAGE:-rustfs/rustfs:latest}"
CLAMAV_IMAGE="${CLAMAV_IMAGE:-clamav/clamav:stable_base-debian}"
RUSTFS_DATA_DIR="${RUSTFS_DATA_DIR:-$ROOT_DIR/.cache/native/rustfs}"
CLAMAV_DATA_DIR="${CLAMAV_DATA_DIR:-$ROOT_DIR/.cache/native/clamav}"
BIN_DIR="$ROOT_DIR/.cache/native/bin"
LOG_DIR="$ROOT_DIR/.cache/native/logs"
DEMO_DATA_DIR="$ROOT_DIR/.cache/native/demo"
SECURITY_CONFIG="${SECURITY_CONFIG:-$ROOT_DIR/config/security.host-native-clamav.yaml}"

STREAM_PID=""
DEMO_PID=""
STARTED_RUSTFS=0
STARTED_CLAMAV=0

cleanup() {
  if [[ -n "$STREAM_PID" ]]; then kill "$STREAM_PID" >/dev/null 2>&1 || true; fi
  if [[ -n "$DEMO_PID" ]]; then kill "$DEMO_PID" >/dev/null 2>&1 || true; fi
  if [[ "${KEEP_CONTAINERS:-0}" != "1" ]]; then
    if [[ "$STARTED_CLAMAV" == "1" ]]; then "$RUNTIME" stop "$CLAMAV_NAME" >/dev/null 2>&1 || true; fi
    if [[ "$STARTED_RUSTFS" == "1" ]]; then "$RUNTIME" stop "$RUSTFS_NAME" >/dev/null 2>&1 || true; fi
  fi
}
trap cleanup EXIT INT TERM

container_running() {
  "$RUNTIME" ps --format '{{.Names}}' | grep -qx "$1"
}

wait_tcp() {
  local host="$1"
  local port="$2"
  local label="$3"
  for _ in {1..180}; do
    if (echo >"/dev/tcp/$host/$port") >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "timeout waiting for $label at $host:$port" >&2
  return 1
}

mkdir -p "$BIN_DIR" "$LOG_DIR" "$DEMO_DATA_DIR" "$RUSTFS_DATA_DIR" "$CLAMAV_DATA_DIR"

echo "==> building host binaries"
cd "$ROOT_DIR"
go build -o "$BIN_DIR/streamuploader" ./cmd/streamuploader
go build -o "$BIN_DIR/demo-app" ./demo/app

if container_running "$RUSTFS_NAME"; then
  echo "==> reusing RustFS container $RUSTFS_NAME"
else
  echo "==> starting RustFS container $RUSTFS_NAME"
  "$RUNTIME" rm -f "$RUSTFS_NAME" >/dev/null 2>&1 || true
  "$RUNTIME" run -d \
    --name "$RUSTFS_NAME" \
    -e RUSTFS_ACCESS_KEY=rustfsadmin \
    -e RUSTFS_SECRET_KEY=rustfsadmin \
    -e RUSTFS_CONSOLE_ENABLE=true \
    -p 9000:9000 \
    -p 9001:9001 \
    -v "$RUSTFS_DATA_DIR:/data" \
    "$RUSTFS_IMAGE" \
    /data >/dev/null
  STARTED_RUSTFS=1
fi
wait_tcp 127.0.0.1 9000 RustFS

if container_running "$CLAMAV_NAME"; then
  echo "==> reusing ClamAV container $CLAMAV_NAME"
else
  echo "==> starting ClamAV container $CLAMAV_NAME"
  "$RUNTIME" rm -f "$CLAMAV_NAME" >/dev/null 2>&1 || true
  "$RUNTIME" run -d \
    --name "$CLAMAV_NAME" \
    -e FRESHCLAM_CHECKS=24 \
    -p 3310:3310 \
    -v "$CLAMAV_DATA_DIR:/var/lib/clamav" \
    "$CLAMAV_IMAGE" >/dev/null
  STARTED_CLAMAV=1
fi
wait_tcp 127.0.0.1 3310 ClamAV

echo "==> starting demo app"
(
  cd "$ROOT_DIR"
  SU_ADDR=:8081 \
  SU_DEMO_DATA_PATH="$DEMO_DATA_DIR/files.json" \
  SU_UPLOAD_BASE_PATH=/api/upload \
  SU_DOWNLOAD_MODE=presigned \
  SU_DELETE_OBJECTS_ON_DELETE=true \
  SU_STREAMUPLOADER_PUBLIC_URL=http://localhost:8080 \
  SU_STREAMUPLOADER_PROXY_URL=http://localhost:8080 \
  SU_BACKEND_CONTROL_URL=http://localhost:8082 \
  SU_S3_BUCKET=stream-upload \
  SU_S3_ENDPOINT=http://localhost:9000 \
  SU_S3_DOWNLOAD_ENDPOINT=http://localhost:9000 \
  SU_S3_REGION=us-east-1 \
  SU_S3_FORCE_PATH_STYLE=true \
  SU_S3_ACCESS_KEY_ID=rustfsadmin \
  SU_S3_SECRET_ACCESS_KEY=rustfsadmin \
  "$BIN_DIR/demo-app"
) >"$LOG_DIR/demo-app.log" 2>&1 &
DEMO_PID=$!
wait_tcp 127.0.0.1 8081 demo-app

echo "==> starting streamuploader with ClamAV"
(
  cd "$ROOT_DIR"
  SU_ADDR=:8080 \
  SU_BACKEND_ADDR=:8082 \
  SU_MODE=simple_fronting_reverse_proxy \
  SU_PUBLIC_BASE_URL=http://localhost:8080 \
  SU_UPLOAD_BASE_PATH=/api/upload \
  SU_APPLICATION_SERVER_URL=http://localhost:8081 \
  SU_ALLOWED_ORIGINS='*' \
  SU_S3_BUCKET=stream-upload \
  SU_S3_ENDPOINT=http://localhost:9000 \
  SU_S3_PUBLIC_ENDPOINT=http://localhost:9000 \
  SU_S3_REGION=us-east-1 \
  SU_S3_FORCE_PATH_STYLE=true \
  SU_S3_PUBLIC_READ=true \
  SU_S3_ACCESS_KEY_ID=rustfsadmin \
  SU_S3_SECRET_ACCESS_KEY=rustfsadmin \
  SU_ALLOW_FRONTEND_FILE_ACCESS=true \
  SU_ENABLE_SHARED_KEY=true \
  SU_SHARED_KEY_BITS=128 \
  SU_MAX_ARCHIVE_FILES=100 \
  SU_MAX_ARCHIVE_BYTES=1073741824 \
  SU_SECURITY_CONFIG="$SECURITY_CONFIG" \
  "$BIN_DIR/streamuploader"
) >"$LOG_DIR/streamuploader.log" 2>&1 &
STREAM_PID=$!
wait_tcp 127.0.0.1 8080 streamuploader

cat <<EOF

Host-native stack is running with ClamAV.

Demo through streamuploader:
  http://localhost:8080/

Direct services:
  streamuploader public  http://localhost:8080
  streamuploader backend http://localhost:8082
  demo app               http://localhost:8081
  RustFS API             http://localhost:9000
  RustFS console         http://localhost:9001
  ClamAV                 127.0.0.1:3310

Logs:
  tail -f "$LOG_DIR/streamuploader.log" "$LOG_DIR/demo-app.log"

Press Ctrl+C to stop.
EOF

wait "$STREAM_PID" "$DEMO_PID"
