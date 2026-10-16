#!/usr/bin/env bash
# Verified module download, then go test with a coverage profile.
# Env: TEST_FLAGS (several flags, word-split on purpose) COVERAGE_FILE PACKAGES
set -euo pipefail

go mod download
go mod verify
# shellcheck disable=SC2086 # TEST_FLAGS holds several flags
go test $TEST_FLAGS -coverprofile="$COVERAGE_FILE" "$PACKAGES"
