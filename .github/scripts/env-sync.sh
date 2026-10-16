#!/usr/bin/env bash
# Runner side of the env job: brings the app settings to the server (`server`
# ssh alias, see setup-ssh.sh). Two independent choices (inputs of _env.yml):
#
# ENV_SOURCE: where the values come from
#   server  managed on the server by hand; only checked here
#   github  APP_SECRETS (GitHub environment secret, dotenv text), optionally
#           after <ENV_DIR>/<ENVIRONMENT>.env (non-secret, in git)
#   sops    <ENV_DIR>/<ENVIRONMENT>.sops.env (encrypted in git), decrypted ON
#           THE SERVER with its age key: the key never leaves the server
#   vault   nothing from CI: Vault Agent on the server renders the secrets
#           (needs ENV_DELIVERY=vault); only checked here
#
# ENV_DELIVERY: how secret keys (ENV_SECRET_KEYS) land on the server, see env-remote.sh
#   dotenv | files | systemd-creds | vault
#
# Secrets only travel over ssh stdin, never on a command line or in a CI file.
# Env: ENV_SOURCE ENV_DELIVERY ENV_DIR ENVIRONMENT DEPLOY_DIR ENV_REQUIRED
#      ENV_SECRET_KEYS APP_SECRETS AGE_KEY_FILE CREDS_CMD CREDS_DIR VAULT_DIR
# ssh command strings expand on the runner on purpose (names/paths only).
# shellcheck disable=SC2029
set -euo pipefail

: "${ENV_SOURCE:?}" "${ENVIRONMENT:?}" "${DEPLOY_DIR:?}"
ENV_DELIVERY="${ENV_DELIVERY:-dotenv}" ENV_DIR="${ENV_DIR:-}"

case "$ENV_DELIVERY" in dotenv|files|systemd-creds|vault) ;; *)
  echo "::error::env.delivery must be dotenv, files, systemd-creds or vault, got: $ENV_DELIVERY"; exit 1 ;;
esac
if [ "$ENV_SOURCE" = vault ] && [ "$ENV_DELIVERY" != vault ]; then
  echo "::error::env.source=vault needs env.delivery=vault"; exit 1
fi
if [ "$ENV_DELIVERY" = systemd-creds ] && [ -z "${CREDS_CMD:-}" ]; then
  echo "::error::env.delivery=systemd-creds needs env.creds_cmd"; exit 1
fi

# Settings for the server script (names and paths only, no secrets).
VARS="DIR='$DEPLOY_DIR' DELIVERY='$ENV_DELIVERY' SECRET_KEYS='${ENV_SECRET_KEYS:-}' \
REQUIRED='${ENV_REQUIRED:-}' CREDS_CMD='${CREDS_CMD:-}' CREDS_DIR='${CREDS_DIR:-}' VAULT_DIR='${VAULT_DIR:-}'"
HANDLER="$DEPLOY_DIR/.env-sync.sh"

ssh server "mkdir -p '$DEPLOY_DIR'"
scp "$(dirname "$0")/env-remote.sh" "server:$HANDLER"

case "$ENV_SOURCE" in
  server|vault)
    echo "Settings are managed on the server ($ENV_SOURCE); checking only"
    ;;

  github)
    [ -n "${APP_SECRETS:-}" ] || { echo "::error::APP_SECRETS is empty in the $ENVIRONMENT environment"; exit 1; }
    PUBLIC="$ENV_DIR/$ENVIRONMENT.env"
    # Secrets last: on a duplicate key the secret wins.
    {
      if [ -n "$ENV_DIR" ] && [ -f "$PUBLIC" ]; then cat "$PUBLIC"; echo; fi
      printf '%s\n' "$APP_SECRETS"
    } | ssh server "$VARS bash '$HANDLER' write"
    ;;

  sops)
    : "${AGE_KEY_FILE:?env.age_key_file (path on the server) is required for env.source=sops}"
    ENC="$ENV_DIR/$ENVIRONMENT.sops.env"
    [ -f "$ENC" ] || { echo "::error::$ENC not found"; exit 1; }
    scp "$ENC" "server:$DEPLOY_DIR/.env.sops"
    # Decrypted straight into the handler: plaintext never lands in a file.
    ssh server "command -v sops >/dev/null || { echo '::error::sops is not installed on the server'; exit 1; }
      SOPS_AGE_KEY_FILE='$AGE_KEY_FILE' sops --decrypt --input-type dotenv --output-type dotenv '$DEPLOY_DIR/.env.sops' \
        | $VARS bash '$HANDLER' write"
    ;;

  *)
    echo "::error::env.source must be server, github, sops or vault, got: $ENV_SOURCE"; exit 1 ;;
esac

ssh server "$VARS bash '$HANDLER' check"
