#!/usr/bin/env bash
# Runs the m1 completion checks of the Drive against a running compose kit
# written by `drive init --target compose` (acceptance compose-roundtrip in
# .knowledge/concepts/requirement/drive-init-subcommand.yaml).
#
#   scripts/drive-compose-check.sh [http://localhost:8080]
#
# With DRIVE_CHECK_REBUILD=1 and the kit directory as the working directory,
# it also stops the stack, deletes the index volume and checks that the
# restarted Drive rebuilds identical results from the bucket.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="${1:-http://localhost:8080}"
PHRASE="%E5%A4%A7%E9%98%AA%E6%B9%BE%E3%81%AE%E4%BA%BA%E5%B7%A5%E5%B3%B6" # 大阪湾の人工島, only on page 3 of kuko-3pages.pdf

wait_health() {
  for _ in $(seq 1 90); do
    curl -fsS "$BASE/healthz" >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "$BASE/healthz never answered" >&2
  return 1
}
search() { curl -fsS "$BASE/api/drive/search?$1"; }
page_hit() {
  # Prints "<file_id> <page>" of the best hit for the phrase, or nothing.
  search "q=$PHRASE" | python3 -c 'import json,sys
d=json.load(sys.stdin); h=d["hits"][0] if d["hits"] else None
print(h["file"]["file_id"], h["page"]) if h else print("")'
}
wait_page_hit() {
  local out=""
  for _ in $(seq 1 60); do
    out=$(page_hit) || true
    [[ -n "$out" ]] && { echo "$out"; return 0; }
    sleep 2
  done
  echo "no hit for the page-3 phrase after 120s" >&2
  return 1
}

echo "== health"
wait_health
echo "== seed three fixtures through the public API"
"$ROOT/scripts/drive-demo-seed.sh" "$BASE"
echo "== check 1: the files appear in the inbox folder at once (journal overlay)"
search "tag=inbox&sort=name" | python3 -c 'import json,sys
d=json.load(sys.stdin); names=[h["file"]["name"] for h in d["hits"]]
assert d["total"]==3, d
print("inbox:", names)'
echo "== check 2: the page-3 phrase returns the PDF with page 3"
hit=$(wait_page_hit)
PDF_ID=${hit%% *}
PAGE=${hit##* }
[[ "$PAGE" == "3" ]] || { echo "best page is $PAGE, want 3" >&2; exit 1; }
echo "hit: $hit"
echo "== check 1b: the overlay drains once the indexer has folded the events"
ov=""
for _ in $(seq 1 30); do
  ov=$(search "tag=inbox&sort=name" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("overlay",0), d["total"])')
  [[ "$ov" == "0 3" ]] && break
  sleep 2
done
[[ "$ov" == "0 3" ]] || { echo "overlay never drained: $ov" >&2; exit 1; }
echo "facets /tags:" "$(curl -fsS "$BASE/api/drive/facets?path=/tags" | python3 -c 'import json,sys; print([(c["name"], c["count"]) for c in json.load(sys.stdin)["children"]])')"
echo "== check 4 (server side): preview range, thumbnail, UI"
code=$(curl -s -o /dev/null -w '%{http_code} %{content_type}' -H 'Range: bytes=0-99' "$BASE/api/drive/files/$PDF_ID/preview")
echo "preview range: $code"; [[ "$code" == 206* ]]
PHOTO_ID=$(search "q=photo" | python3 -c 'import json,sys; d=json.load(sys.stdin); print([h["file"]["file_id"] for h in d["hits"] if h["file"]["name"]=="photo.jpg"][0])')
code=$(curl -s -o /dev/null -w '%{http_code} %{content_type}' "$BASE/api/drive/files/$PHOTO_ID/thumbnail")
echo "thumbnail: $code"; [[ "$code" == 200\ image/* ]]
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")
echo "ui index: $code"; [[ "$code" == 200 ]]
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ui/app.js")
echo "ui bundle: $code"; [[ "$code" == 200 ]]

if [[ "${DRIVE_CHECK_REBUILD:-0}" == "1" ]]; then
  echo "== check 3: delete the index volume, restart, rebuild from the bucket"
  [[ -f compose.yaml && -f .env ]] || { echo "run from the kit directory for the rebuild check" >&2; exit 1; }
  project=$(grep '^COMPOSE_PROJECT_NAME=' .env | cut -d= -f2)
  docker compose down >/dev/null
  docker volume rm "${project}_drive-index" >/dev/null
  docker compose up -d >/dev/null
  wait_health
  after=$(wait_page_hit)
  [[ "$after" == "$PDF_ID 3" ]] || { echo "rebuild changed the hit: $after" >&2; exit 1; }
  search "tag=inbox&sort=name" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["total"]==3, d'
  echo "rebuild: identical hit and folder"
fi
echo "ALL CHECKS PASSED"
