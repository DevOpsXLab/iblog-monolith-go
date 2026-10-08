#!/usr/bin/env bash
# Sends CI result to the Telegram dev group topic.
# Env: BOT_TOKEN CHAT_ID TOPIC_ID LINT TYPECHECK TEST BUILD REPO BRANCH ACTOR
#      REPO_URL RUN_URL COMMIT_MSG COMMIT_URL
set -euo pipefail

if [ -z "${BOT_TOKEN:-}" ] || [ -z "${CHAT_ID:-}" ]; then
  echo "BOT_TOKEN/CHAT_ID missing - Telegram message not sent"
  exit 0
fi

escape_html() {
  printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'
}

icon() {
  case "$1" in
    success) echo "✅" ;;
    failure) echo "❌" ;;
    cancelled) echo "⏹" ;;
    skipped) echo "⏭" ;;
    *) echo "❔" ;;
  esac
}

# Failed jobs: name, failed step, last log lines with "error".
ERRORS=""
if [ -n "${GH_TOKEN:-}" ] && [ -n "${RUN_ID:-}" ]; then
  FAILED=$(gh api "repos/${REPO}/actions/runs/${RUN_ID}/jobs" \
    --jq '.jobs[] | select(.conclusion=="failure") | "\(.id)\t\(.name)\t\([.steps[] | select(.conclusion=="failure") | .name] | join(", "))"' 2>/dev/null || true)
  while IFS=$'\t' read -r JOB_ID JOB_NAME STEP_NAME; do
    [ -z "$JOB_ID" ] && continue
    # continue-on-error jobs report needs.<job>.result == success; use the real conclusion.
    case "$JOB_NAME" in
      Lint) LINT=failure ;;
      Typecheck) TYPECHECK=failure ;;
      Test) TEST=failure ;;
      *[Bb]uild*) BUILD=failure ;;
    esac
    LOG=$(gh api "repos/${REPO}/actions/jobs/${JOB_ID}/logs" 2>/dev/null \
      | sed -E 's/^[0-9T:.-]+Z //' \
      | grep -iE 'error|failed|✕|FAIL' | grep -v '##\[' | tail -n 8 | cut -c1-200 || true)
    ERRORS+=$'\n'"❌ <b>$(escape_html "$JOB_NAME")</b> → $(escape_html "$STEP_NAME")"
    if [ -n "$LOG" ]; then
      ERRORS+=$'\n'"<pre>$(escape_html "$LOG")</pre>"
    fi
  done <<< "$FAILED"
fi
# Telegram limit 4096 chars.
ERRORS=$(printf '%s' "$ERRORS" | cut -c1-2500)

FAILED_STAGES=""
[ "$LINT" != "success" ] && FAILED_STAGES+="Lint, "
[ "$TYPECHECK" != "success" ] && FAILED_STAGES+="Typecheck, "
[ "$TEST" != "success" ] && FAILED_STAGES+="Test, "
[ "$BUILD" != "success" ] && FAILED_STAGES+="Docker build, "
if [ -z "$FAILED_STAGES" ]; then
  STATUS="✅ <b>CI passed</b>"
else
  STATUS="❌ <b>CI failed: ${FAILED_STAGES%, }</b>"
fi

SAFE_REPO=$(escape_html "$REPO")
SAFE_BRANCH=$(escape_html "$BRANCH")
SAFE_ACTOR=$(escape_html "$ACTOR")
SAFE_MSG=$(escape_html "$(printf '%s' "${COMMIT_MSG:-}" | head -n1)")

TEXT=$(cat <<EOF
${STATUS}

📦 Repo: <a href="${REPO_URL}">${SAFE_REPO}</a>
🌿 Branch: <code>${SAFE_BRANCH}</code>
👤 By: <b>${SAFE_ACTOR}</b>
📝 Commit: <a href="${COMMIT_URL:-$REPO_URL}">${SAFE_MSG}</a>

$(icon "$LINT") Lint
$(icon "$TYPECHECK") Typecheck
$(icon "$TEST") Test
$(icon "$BUILD") Docker build

${ERRORS}

🔗 <a href="${RUN_URL}">View run</a>
EOF
)

ARGS=(-d chat_id="${CHAT_ID}" -d parse_mode="HTML" -d disable_web_page_preview="true")
if [ -n "${TOPIC_ID:-}" ]; then
  ARGS+=(-d message_thread_id="${TOPIC_ID}")
fi

curl -sS -o /dev/null -w "HTTP_STATUS:%{http_code}\n" -X POST \
  "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" \
  "${ARGS[@]}" \
  --data-urlencode "text=${TEXT}"
