#!/usr/bin/env bash
# Sends the CI result to Telegram.
# Env (required): BOT_TOKEN, CHAT_ID, LINT, TEST, BUILD, REPO, REPO_URL,
#                 BRANCH, ACTOR, EVENT, RUN_URL
# Env (optional): COMMIT_MSG, COMMIT_URL (empty on some pushes, e.g. branch deletes)
set -euo pipefail

: "${BOT_TOKEN:?BOT_TOKEN is required}"
: "${CHAT_ID:?CHAT_ID is required}"
: "${LINT:?LINT is required}"
: "${TEST:?TEST is required}"
: "${BUILD:?BUILD is required}"
: "${REPO:?REPO is required}"
: "${BRANCH:?BRANCH is required}"
: "${ACTOR:?ACTOR is required}"
: "${EVENT:?EVENT is required}"
: "${REPO_URL:?REPO_URL is required}"
: "${RUN_URL:?RUN_URL is required}"

icon() {
  case "$1" in
    success)   echo "✅" ;;
    failure)   echo "❌" ;;
    cancelled) echo "🚫" ;;
    skipped)   echo "⏭" ;;
    *)         echo "❔" ;;
  esac
}

# Overall: any failure wins, then any cancel, else success.
results=" $LINT $TEST $BUILD "
if [[ "$results" == *" failure "* ]]; then
  status="❌ CI failed"
elif [[ "$results" == *" cancelled "* ]]; then
  status="🚫 CI cancelled"
else
  status="✅ CI passed"
fi

# Telegram caps a message at 4096 chars; keep the commit message well under that.
msg=$(printf '%s' "${COMMIT_MSG:-}" | head -c 1500)

text="$status

Repo: $REPO
$REPO_URL
Branch: $BRANCH
Event: $EVENT
Author: $ACTOR

Commit: ${COMMIT_URL:-}
Message:
$msg

Stages:
$(icon "$LINT") lint: $LINT
$(icon "$TEST") test: $TEST
$(icon "$BUILD") build: $BUILD

Run: $RUN_URL"

curl -sS --fail --max-time 10 \
  -X POST "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" \
  --data-urlencode "chat_id=${CHAT_ID}" \
  --data-urlencode "text=${text}" \
  -d disable_web_page_preview=true \
  > /dev/null
