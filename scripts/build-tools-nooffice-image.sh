#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
IMAGE=${1:-streamuploader:tools-nooffice}

APKO_CONFIG=tools.nooffice.apko.yaml exec "$ROOT_DIR/scripts/build-tools-image.sh" "$IMAGE"
