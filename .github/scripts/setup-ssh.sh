#!/usr/bin/env bash
# Writes the SSH key, pinned known_hosts and a `server` host alias.
# Host key comes from KNOWN_HOSTS (checked once by hand), never ssh-keyscan:
# trusting the first key the runner sees would allow a MITM.
# Env: SERVER_HOST SERVER_USER SSH_KEY KNOWN_HOSTS
set -euo pipefail

missing=""
for k in SERVER_HOST SERVER_USER SSH_KEY KNOWN_HOSTS; do
  [ -z "${!k:-}" ] && missing="$missing $k"
done
if [ -n "$missing" ]; then
  echo "::error::missing in the ${ENVIRONMENT:-current} environment:$missing"
  exit 1
fi

install -d -m 700 ~/.ssh
printf '%s\n' "$SSH_KEY" > ~/.ssh/id
printf '%s\n' "$KNOWN_HOSTS" > ~/.ssh/known_hosts
chmod 600 ~/.ssh/id ~/.ssh/known_hosts
printf 'Host server\n  HostName %s\n  User %s\n  IdentityFile ~/.ssh/id\n  StrictHostKeyChecking yes\n' \
  "$SERVER_HOST" "$SERVER_USER" > ~/.ssh/config
