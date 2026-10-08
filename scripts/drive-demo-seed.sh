#!/usr/bin/env bash
# Uploads the Drive test fixtures through the public API of a running Drive.
# Usage: scripts/drive-demo-seed.sh [http://localhost:8090]
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="${1:-http://localhost:8090}"
JAR="$(mktemp)"
trap 'rm -f "$JAR"' EXIT

upload() {
  local path="$1" type="$2" tags="$3" name
  name="$(basename "$path")"
  local size
  size=$(stat -f%z "$path" 2>/dev/null || stat -c%s "$path")
  local key_json
  key_json=$(curl -sS -c "$JAR" -b "$JAR" -H 'Content-Type: application/json' \
    -d "{\"file_name\":\"$name\",\"content_type\":\"$type\",\"size_bytes\":$size}" "$BASE/api/upload/keys")
  local upload_key
  upload_key=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["upload_key"])' <<<"$key_json")
  curl -sS -c "$JAR" -b "$JAR" -X PUT -H "Content-Type: $type" --data-binary "@$path" \
    "$BASE/api/upload/keys/$upload_key/content" >/dev/null
  local wait_json item
  for _ in $(seq 1 30); do
    wait_json=$(curl -sS -c "$JAR" -b "$JAR" -H 'Content-Type: application/json' \
      -d "{\"upload_keys\":[\"$upload_key\"],\"timeout_seconds\":2}" "$BASE/api/upload/wait")
    item=$(python3 -c 'import json,sys
d=json.load(sys.stdin); it=(d.get("items") or [None])[0]
print(json.dumps(it) if it and it.get("status")=="uploaded" else "")' <<<"$wait_json")
    [[ -n "$item" ]] && break
  done
  [[ -n "$item" ]] || { echo "upload of $name did not finish: $wait_json" >&2; return 1; }
  python3 - "$item" "$tags" <<'PY' > /tmp/drive-register.json
import json,sys
print(json.dumps({"upload": json.loads(sys.argv[1]), "tags": [t for t in sys.argv[2].split(",") if t]}))
PY
  curl -sS -c "$JAR" -b "$JAR" -H 'Content-Type: application/json' --data-binary @/tmp/drive-register.json "$BASE/api/drive/files" \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); print("registered", d["file_id"], d["name"], d.get("tags"))'
}

upload "$ROOT_DIR/internal/docpreview/testdata/sample.docx" "application/vnd.openxmlformats-officedocument.wordprocessingml.document" "inbox,仕様書"
upload "$ROOT_DIR/drive/testdata/kuko-3pages.pdf" "application/pdf" "inbox,旅行/空港"
upload "$ROOT_DIR/drive/testdata/photo.jpg" "image/jpeg" "inbox,写真/2026"
echo "seeded $BASE"
