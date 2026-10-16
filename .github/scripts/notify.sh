#!/usr/bin/env bash
# Sends a CI or CD result to Telegram: runs notify-<MODE>.sh next to it.
# Env: MODE (ci | cd), the rest see notify-ci.sh / notify-cd.sh
set -euo pipefail

case "${MODE:-}" in
  ci|cd) exec bash "$(dirname "$0")/notify-$MODE.sh" ;;
  *) echo "::error::mode must be ci or cd, got: ${MODE:-}"; exit 1 ;;
esac
