#!/usr/bin/env bash
# Loads a scanned `docker save` tarball and pushes it under the immutable tag.
# Env: TARBALL LOCAL (image name inside the tarball) IMAGE_NAME TARGET TAG ENVIRONMENT
set -euo pipefail

[ -n "${IMAGE_NAME:-}" ] || { echo "::error::IMAGE_NAME is not set in the $ENVIRONMENT environment"; exit 1; }
IMAGE="$IMAGE_NAME-$TARGET:$TAG"

docker load --input "$TARBALL"
docker tag "$LOCAL" "$IMAGE"
docker push "$IMAGE"
