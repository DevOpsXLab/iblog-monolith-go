#!/usr/bin/env bash
# Sends the CD result to Telegram. Missing bot settings only warn:
# a Telegram problem must never mark a good deploy as failed.
# Env: BOT_TOKEN CHAT_ID TOPIC_ID ENVIRONMENT TAG ACTOR COMMIT_MSG RUN_URL
#      RESULTS (caller's toJSON(needs); every job except meta is a stage)
set -euo pipefail

if [ -z "${BOT_TOKEN:-}" ] || [ -z "${CHAT_ID:-}" ]; then
  echo "::warning::Telegram not configured"
  exit 0
fi

icon() {
  case "$1" in
    success) echo "✅" ;;
    failure) echo "❌" ;;
    cancelled) echo "⏹" ;;
    *) echo "⏭" ;;
  esac
}

escape_html() {
  printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'
}

RESULTS=${RESULTS:-'{}'}
STAGE_LINE=""
while IFS=$'\t' read -r JOB RESULT; do
  [ -z "$JOB" ] && continue
  STAGE_LINE+="$(icon "$RESULT") ${JOB^}  "
done < <(jq -r 'to_entries[] | select(.key != "meta") | "\(.key)\t\(.value.result)"' <<<"$RESULTS")

DEPLOY=$(jq -r '.deploy.result // empty' <<<"$RESULTS")
if [ "$DEPLOY" = success ]; then
  STATUS="✅ <b>Deployed to ${ENVIRONMENT}</b>"
else
  STATUS="❌ <b>Deploy to ${ENVIRONMENT} failed</b>"
fi

TEXT=$(cat <<MSG
${STATUS} <code>${TAG}</code>

👤 $(escape_html "$ACTOR")
📝 $(escape_html "$(printf '%s' "$COMMIT_MSG" | head -n1)")

${STAGE_LINE% }

🔗 <a href="${RUN_URL}">View run</a>
MSG
)

ARGS=(-d chat_id="$CHAT_ID" -d parse_mode=HTML -d disable_web_page_preview=true)
if [ -n "${TOPIC_ID:-}" ]; then
  ARGS+=(-d message_thread_id="$TOPIC_ID")
fi

curl -fsS --retry 3 --retry-delay 2 --max-time 30 \
  "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" \
  "${ARGS[@]}" --data-urlencode "text=${TEXT}" >/dev/null \
  || echo "::warning::Telegram sendMessage failed"
