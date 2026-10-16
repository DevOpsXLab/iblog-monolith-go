#!/usr/bin/env bash
# Runner side of a deploy: pipes deploy-remote.sh (plain docker) to the server
# over the `server` alias (setup-ssh.sh first). Only names and paths go on the
# ssh command line; settings stay in $DEPLOY_DIR/.env on the server.
# Env: DEPLOY_DIR IMAGE_NAME TAG NETWORK PREFIX MIGRATE_SERVICE MIGRATE_MODE APP_SERVICES
#      SERVICE_ARGS HEALTH_TIMEOUT KEEP_TAGS LOG_TAIL
set -euo pipefail

[[ "$TAG" =~ ^[0-9a-f]{7,40}$ ]] || { echo "::error::invalid tag: $TAG"; exit 1; }
[ -n "$IMAGE_NAME" ] || { echo "::error::IMAGE_NAME is not set in the environment"; exit 1; }
SERVICE_ARGS="${SERVICE_ARGS:-}"
[ -n "$SERVICE_ARGS" ] || SERVICE_ARGS='{}'
# JSON -> "<service><TAB><args>" lines, base64 so it survives the ssh quoting.
LINES=$(jq -r 'to_entries[] | "\(.key)\t\(.value)"' <<<"$SERVICE_ARGS") \
  || { echo "::error::service-args is not a JSON object of strings"; exit 1; }
ARGS_B64=$(printf '%s' "$LINES" | base64 -w0)

# shellcheck disable=SC2029 # values expand on the runner (intended)
ssh server "DIR='$DEPLOY_DIR' IMAGE_NAME='$IMAGE_NAME' TAG='$TAG' NETWORK='$NETWORK' PREFIX='$PREFIX' \
  MIGRATE='$MIGRATE_SERVICE' MIGRATE_MODE='${MIGRATE_MODE:-up}' APPS='$APP_SERVICES' ARGS_B64='$ARGS_B64' \
  HEALTH_TIMEOUT='$HEALTH_TIMEOUT' KEEP_TAGS='$KEEP_TAGS' LOG_TAIL='$LOG_TAIL' bash -s" \
  < "$(dirname "$0")/deploy-remote.sh"
