#!/usr/bin/env bash
# Server side of a deploy: plain docker, no compose. Piped over ssh by
# deploy.sh (not run on the runner); needs only docker on the server, the
# deploy user in the docker group and a registry login (~/.docker/config.json).
#
#   pull -> migrate (one-shot `docker run --rm`) -> each app: replace the
#   container, wait healthy -> rollback the apps that changed on failure
#
# MIGRATE_MODE: up (normal release) | skip (rollback, keep schema) |
#   down (rollback with schema): the OLD image (TAG) prints its newest
#   migration (`-latest`), the LIVE image (.tag, it has the Down SQL of the
#   newer migrations) runs goose down to it (`-down-to N`). Destructive:
#   columns/tables added since are dropped with their data.
#
# Rollback is instant: the old container is only stopped and renamed to
# <name>-prev while the new one starts, and removed after the whole release is
# healthy. Migrations are not reverted: write them expand/contract.
# $DIR/.tag holds the live tag. Settings: --env-file $DIR/.env; secret files
# in $DIR/secrets are mounted read-only at the same path (KEY_FILE points there).
#
# Env: DIR IMAGE_NAME TAG NETWORK PREFIX MIGRATE MIGRATE_MODE APPS ARGS_B64
#      HEALTH_TIMEOUT KEEP_TAGS LOG_TAIL
#   APPS      space-separated services, e.g. "api worker"
#   ARGS_B64  base64 of "<service><TAB><extra docker run args>" lines (deploy.sh
#             builds it from the service-args JSON, so the server needs no jq)
set -euo pipefail

: "${DIR:?}" "${IMAGE_NAME:?}" "${TAG:?}" "${APPS:?}"
NETWORK="${NETWORK:-}" PREFIX="${PREFIX:-}" MIGRATE="${MIGRATE:-}"
ARGS=$(printf '%s' "${ARGS_B64:-}" | base64 -d)
MIGRATE_MODE="${MIGRATE_MODE:-up}"
case "$MIGRATE_MODE" in up|skip|down) ;; *) echo "::error::MIGRATE_MODE must be up, skip or down"; exit 1 ;; esac
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}" KEEP_TAGS="${KEEP_TAGS:-3}" LOG_TAIL="${LOG_TAIL:-100}"

cd "$DIR"
[ -f .env ] || { echo "::error::$DIR/.env is missing (env job writes it)"; exit 1; }
PREV=$(cat .tag 2>/dev/null || true)
echo "Previous tag: ${PREV:-none}, new tag: $TAG"

image() { echo "$IMAGE_NAME-$1:$2"; } # service tag
name() { echo "$PREFIX$1"; }          # container name
extra() { awk -F'\t' -v s="$1" '$1 == s { print $2; exit }' <<<"$ARGS"; }

# Same options for every app and migrate container.
common=(--env-file .env --log-opt max-size=10m --log-opt max-file=5)
[ -n "$NETWORK" ] && common+=(--network "$NETWORK")
[ -d secrets ] && common+=(-v "$DIR/secrets:$DIR/secrets:ro")

start() { # service tag
  local args
  read -r -a args <<<"$(extra "$1")"
  docker run -d --name "$(name "$1")" --restart unless-stopped \
    "${common[@]}" "${args[@]}" "$(image "$1" "$2")" >/dev/null
}

# Healthy = the image HEALTHCHECK says healthy. An image without one counts
# as healthy when it is still running after a 10s grace period.
wait_healthy() { # container
  local end=$((SECONDS + HEALTH_TIMEOUT)) s has
  has=$(docker inspect -f '{{if .State.Health}}y{{end}}' "$1" 2>/dev/null || true)
  [ -n "$has" ] || { sleep 10; [ "$(docker inspect -f '{{.State.Status}}' "$1" 2>/dev/null)" = running ]; return; }
  while [ $SECONDS -lt $end ]; do
    s=$(docker inspect -f '{{.State.Health.Status}} {{.State.Status}}' "$1" 2>/dev/null || echo "missing missing")
    case "$s" in
      healthy*) return 0 ;;
      unhealthy*|*exited|*dead|missing*) return 1 ;;
    esac
    sleep 3
  done
  return 1
}

[ -n "$MIGRATE" ] || MIGRATE_MODE=skip
if [ "$MIGRATE_MODE" = down ] && { [ -z "$PREV" ] || [ "$PREV" = "$TAG" ]; }; then
  echo "::error::schema rollback needs a live tag different from $TAG (.tag: ${PREV:-none})"; exit 1
fi

# 1. Pull everything first: a registry problem fails before anything changes.
for svc in $APPS; do docker pull -q "$(image "$svc" "$TAG")"; done
case "$MIGRATE_MODE" in
  up) docker pull -q "$(image "$MIGRATE" "$TAG")" ;;
  down) docker pull -q "$(image "$MIGRATE" "$TAG")"; docker pull -q "$(image "$MIGRATE" "$PREV")" ;;
esac

# 2. Schema (one-shot containers); a failure stops before any app changes.
if [ "$MIGRATE_MODE" != skip ]; then
  read -r -a margs <<<"$(extra "$MIGRATE")"
  migrate() { # tag args...
    local t="$1"; shift
    docker run --rm --name "$(name "$MIGRATE")-$t" "${common[@]}" "${margs[@]}" "$(image "$MIGRATE" "$t")" "$@"
  }
  if [ "$MIGRATE_MODE" = up ]; then
    migrate "$TAG"
  else
    target=$(docker run --rm "$(image "$MIGRATE" "$TAG")" -latest)
    [[ "$target" =~ ^[0-9]+$ ]] || { echo "::error::$TAG migrate -latest printed '$target'"; exit 1; }
    echo "Schema rollback: down to migration $target (with the $PREV image)"
    migrate "$PREV" -down-to "$target"
  fi
fi

# 3. Apps, one by one; remember what changed for the rollback.
changed=()
rollback() {
  local svc c
  echo "Rolling back: ${changed[*]:-nothing}"
  for svc in "${changed[@]}"; do
    c=$(name "$svc")
    docker logs --tail "$LOG_TAIL" "$c" 2>&1 | sed "s/^/[$svc] /" || true
    docker rm -f "$c" >/dev/null 2>&1 || true
    if docker inspect "$c-prev" >/dev/null 2>&1; then
      docker rename "$c-prev" "$c" && docker start "$c" >/dev/null
      wait_healthy "$c" && echo "$svc: back on previous container" || echo "$svc: previous container is unhealthy too"
    fi
  done
  exit 1
}

for svc in $APPS; do
  c=$(name "$svc")
  docker rm -f "$c-prev" >/dev/null 2>&1 || true
  if docker inspect "$c" >/dev/null 2>&1; then
    docker stop -t 30 "$c" >/dev/null
    docker rename "$c" "$c-prev"
  fi
  changed+=("$svc")
  start "$svc" "$TAG" || rollback
  if wait_healthy "$c"; then echo "$svc: healthy ($TAG)"; else echo "$svc: not healthy"; rollback; fi
done

# 4. Release is healthy: drop the previous containers, record the tag.
for svc in $APPS; do docker rm -f "$(name "$svc")-prev" >/dev/null 2>&1 || true; done
echo "$TAG" > .tag
echo "Released $TAG"

# 5. Keep the KEEP_TAGS newest tags per image locally (fast rollback by tag).
for svc in $MIGRATE $APPS; do
  docker image ls "$IMAGE_NAME-$svc" --format '{{.Tag}}' | tail -n +$((KEEP_TAGS + 1)) \
    | xargs -r -I{} docker image rm "$IMAGE_NAME-$svc:{}" >/dev/null 2>&1 || true
done
