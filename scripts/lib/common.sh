#!/usr/bin/env bash
# Common shell helpers (Phase 0 scaffold)
set -Eeo pipefail
IFS=$'\n\t'

# Logging
log() { printf '%s %s\n' "[$1]" "${*:2}"; }
log_info() { log INFO "$@"; }
log_warn() { log WARN "$@"; }
log_error() { log ERROR "$@"; }

# Fail with message
_die() { log_error "$@"; exit 1; }

die() { _die "$@"; }

# Retry helper: retry <attempts> <sleep> -- cmd...
retry() {
  local attempts="$1"; shift || true
  local sleepsec="$1"; shift || true
  local n=1
  until "$@"; do
    if (( n >= attempts )); then return 1; fi
    n=$((n+1)); sleep "$sleepsec";
  done
}

# Require env vars
require_env() {
  local missing=0
  for v in "$@"; do
    if [ -z "${!v:-}" ]; then log_error "Missing env: $v"; missing=1; fi
  done
  [ "$missing" -eq 0 ] || return 1
}

# Load .env (KEY=VALUE) if present
load_env() {
  local env_file="${1:-scripts/.env}"
  [ -f "$env_file" ] || return 0
  # shellcheck disable=SC1090
  set -a; . "$env_file"; set +a
}

# YAML/JSON accessors (require yq/jq)
yaml_get() { yq -r "$2" "$1"; }
json_get() { jq -r "$2" "$1"; }
