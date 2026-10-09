#!/usr/bin/env bash
# Runs the Drive all-in-one against the in-memory object store: no RustFS, no
# Docker. Everything is lost when the process exits. Builds the Go binary and,
# when cargo is available and the sidecar is missing, the Rust sidecar.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/.cache/drive/bin"
SIDECAR="${DRIVE_SEARCH_BIN:-$ROOT_DIR/search/target/release/drivesearch}"
PORT="${DRIVE_PORT:-8090}"
mkdir -p "$BIN_DIR"
if [[ ! -x "$SIDECAR" ]]; then
  if command -v cargo >/dev/null 2>&1 || [[ -x "$HOME/.cargo/bin/cargo" ]]; then
    echo "==> building search sidecar"
    (cd "$ROOT_DIR/search" && PATH="$HOME/.cargo/bin:$PATH" cargo build --release)
  else
    echo "drivesearch not found at $SIDECAR and cargo is not installed" >&2
    exit 1
  fi
fi
echo "==> generating framework code"
(cd "$ROOT_DIR" && go tool pw generate)
echo "==> building drive"
(cd "$ROOT_DIR" && go build -o "$BIN_DIR/drive" ./cmd/drive)
echo "==> drive (memory storage) on http://localhost:$PORT"
exec env \
  DRIVE_STORAGE=memory \
  DRIVE_SEARCH_BIN="$SIDECAR" \
  DRIVE_INDEX_DIR="$ROOT_DIR/.cache/drive/index-memory" \
  DRIVE_INDEX_INTERVAL="${DRIVE_INDEX_INTERVAL:-5s}" \
  PORT="$PORT" \
  SU_PUBLIC_BASE_URL="http://localhost:$PORT" \
  SU_THUMBNAILS_ENABLED=true \
  SU_THUMBNAILS_EXECUTION_MODE=sequential \
  SU_TEXT_EXTRACTION_EXECUTION_MODE=sequential \
  SU_DOCUMENT_PROCESSING_EXECUTION_MODE=sequential \
  "$BIN_DIR/drive" "$@"
