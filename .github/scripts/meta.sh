#!/usr/bin/env bash
# Run-wide CD values, written to $GITHUB_OUTPUT: environment, image tag,
# version, whether this run is a manual rollback and the migrate mode.
# Env: ENVIRONMENT TAG_LENGTH ROLLBACK_TAG (empty = normal deploy) ROLLBACK_SCHEMA
set -euo pipefail

TAG=${GITHUB_SHA::$TAG_LENGTH}
ROLLBACK=false
MIGRATE_MODE=up # release: goose up
if [ -n "${ROLLBACK_TAG:-}" ]; then
  # Only an image tag CD pushed before: short or full git sha.
  [[ "$ROLLBACK_TAG" =~ ^[0-9a-f]{7,40}$ ]] || { echo "::error::rollback-tag must be a git sha, got: $ROLLBACK_TAG"; exit 1; }
  TAG=$ROLLBACK_TAG ROLLBACK=true
  # Rollback: keep the schema (expand/contract), or goose down when asked.
  MIGRATE_MODE=${ROLLBACK_SCHEMA:-skip}
  echo "::notice::Rollback of $ENVIRONMENT to $TAG (no build; schema: ${ROLLBACK_SCHEMA:-keep})"
fi

# Version baked into the binaries (main.version) and the OCI label:
# <branch>-<short sha>, e.g. main-8d777fa. Commit is always the full sha.
{
  echo "environment=$ENVIRONMENT"
  echo "tag=$TAG"
  echo "version=${GITHUB_REF_NAME//\//-}-$TAG"
  echo "rollback=$ROLLBACK"
  echo "migrate-mode=$MIGRATE_MODE"
} >> "$GITHUB_OUTPUT"
