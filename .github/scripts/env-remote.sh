#!/usr/bin/env bash
# Server side of env-sync. Uploaded to $DIR/.env-sync.sh by env-sync.sh (no
# secrets in it) and run over ssh:
#
#   write  read dotenv text on stdin; non-secret keys -> $DIR/.env, keys in
#          SECRET_KEYS -> delivered by DELIVERY:
#            dotenv         stay in .env (env vars; simplest, weakest)
#            files          $DIR/secrets/<key> (dir 0700, file 0400) and
#                           KEY_FILE=<path> in .env: app reads the file
#            systemd-creds  piped to `sudo $CREDS_CMD <key>` which encrypts it
#                           with the server's TPM (/etc/credstore.encrypted);
#                           plaintext never touches the disk
#   check  every REQUIRED key is in .env or delivered (file / credential /
#          vault-rendered file). Names only; values are never printed.
#
# Values are taken as written after the first '=' (no quote processing).
# Env: DIR DELIVERY SECRET_KEYS REQUIRED CREDS_CMD CREDS_DIR VAULT_DIR
set -euo pipefail

MODE="${1:?usage: .env-sync.sh write|check}"
DELIVERY="${DELIVERY:-dotenv}"
SECRET_KEYS=" ${SECRET_KEYS:-} "
CREDS_DIR="${CREDS_DIR:-/etc/credstore.encrypted}"
VAULT_DIR="${VAULT_DIR:-$DIR/secrets}"

cd "$DIR"
umask 077
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }
is_secret() { [[ "$SECRET_KEYS" == *" $1 "* ]]; }

deliver() { # key value
  local f
  case "$DELIVERY" in
    files)
      install -d -m 700 secrets
      f="secrets/$(lower "$1")"
      printf '%s' "$2" > "$f.new" && chmod 400 "$f.new" && mv -f "$f.new" "$f"
      # Point the app at it: KEY_FILE in .env (config reads KEY_FILE when KEY is unset).
      printf '%s_FILE=%s\n' "$1" "$DIR/$f" >> .env.new
      ;;
    systemd-creds)
      printf '%s' "$2" | sudo "$CREDS_CMD" "$1"
      ;;
  esac
}

write() {
  local line key value n=0 s=0
  : > .env.new
  while IFS= read -r line || [ -n "$line" ]; do
    [[ "$line" =~ ^[[:space:]]*(#|$) ]] && continue
    key="${line%%=*}" value="${line#*=}"
    [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || { echo "::error::bad dotenv line (key '$key')"; exit 1; }
    if [ "$DELIVERY" != dotenv ] && is_secret "$key"; then
      deliver "$key" "$value"; s=$((s + 1))
    else
      printf '%s=%s\n' "$key" "$value" >> .env.new; n=$((n + 1))
    fi
  done
  chmod 600 .env.new && mv -f .env.new .env
  echo ".env: $n keys; secrets delivered as $DELIVERY: $s"
}

check() {
  local k missing="" f
  touch .env && chmod 600 .env
  for k in ${REQUIRED:-}; do
    grep -q "^$k=." .env && continue
    f=$(lower "$k")
    case "$DELIVERY" in
      files)         [ -s "secrets/$f" ] && continue ;;
      systemd-creds) [ -s "$CREDS_DIR/$f.cred" ] && continue ;;
      vault)         [ -s "$VAULT_DIR/$f" ] && continue ;;
    esac
    missing="$missing $k"
  done
  [ -z "$missing" ] || { echo "::error::missing or empty ($DELIVERY):$missing"; exit 1; }
  echo "settings ok (delivery=$DELIVERY)"
}

case "$MODE" in
  write) write ;;
  check) check ;;
  *) echo "usage: .env-sync.sh write|check"; exit 2 ;;
esac
