#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
IMAGE=${1:-streamuploader-tools:chainguard}
ARCH=${ARCH:-arm64}
APKO_IMAGE=${APKO_IMAGE:-cgr.dev/chainguard/apko}
GO_IMAGE=${GO_IMAGE:-cgr.dev/chainguard/go:latest-dev}
RUST_IMAGE=${RUST_IMAGE:-cgr.dev/chainguard/rust:latest-dev}
DOCKER=${DOCKER:-docker}
WORK_DIR=${WORK_DIR:-"$ROOT_DIR/.cache/tools-image"}
BASE_REF=streamuploader-tools-apko:local
BASE_IMAGE=$BASE_REF-$ARCH
CID=

cleanup() {
  if [ -n "${CID:-}" ]; then
    "$DOCKER" rm -f "$CID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

rm -rf "$WORK_DIR"
mkdir -p "$WORK_DIR/resvg"

echo "==> building streamuploader for linux/$ARCH"
"$DOCKER" run --rm \
  --user 0:0 \
  -e CGO_ENABLED=1 \
  -e GOCACHE=/tmp/go-build \
  -v "$ROOT_DIR:/src" \
  -w /src \
  --entrypoint /usr/bin/go \
  "$GO_IMAGE" \
  build -o /src/.cache/tools-image/streamuploader ./cmd/streamuploader

echo "==> building resvg"
"$DOCKER" run --rm \
  --user 0:0 \
  -v "$WORK_DIR/resvg:/out" \
  --entrypoint /bin/sh \
  "$RUST_IMAGE" \
  -c 'apk add --no-cache cargo-auditable pkgconf >/dev/null && cargo install resvg --version 0.47.0 --locked --root /out'

echo "==> building apko base rootfs"
"$DOCKER" run --rm \
  -v "$ROOT_DIR:/work" \
  "$APKO_IMAGE" \
  build /work/tools.apko.yaml "$BASE_REF" /work/.cache/tools-image/apko-base.tar --arch "$ARCH"

echo "==> loading apko base image"
"$DOCKER" load -i "$WORK_DIR/apko-base.tar" >/dev/null

echo "==> applying final image changes"
CID=$("$DOCKER" create \
  --user 0:0 \
  --entrypoint /usr/bin/rm \
  "$BASE_IMAGE" \
  -f /bin/sh /busybox/sh /usr/bin/sh /bin/bash /usr/bin/bash)
"$DOCKER" start -a "$CID" >/dev/null
"$DOCKER" cp "$WORK_DIR/streamuploader" "$CID:/usr/local/bin/streamuploader"
"$DOCKER" cp "$WORK_DIR/resvg/bin/resvg" "$CID:/usr/local/bin/resvg"
"$DOCKER" commit \
  --change 'ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
  --change 'ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt' \
  --change 'USER 65532:65532' \
  --change 'WORKDIR /work' \
  --change 'ENTRYPOINT ["/usr/local/bin/streamuploader"]' \
  --change 'CMD ["thumbnail-convert","-h"]' \
  "$CID" \
  "$IMAGE" >/dev/null
"$DOCKER" rm -f "$CID" >/dev/null
CID=

echo "==> built $IMAGE"
