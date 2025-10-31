#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

# Common shell library for CI pipelines
# Provides logging, argument parsing, environment validation and helpers

COMMON_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly COMMON_LIB_DIR
readonly SHARED_VOLUME_PATH="${SHARED_VOLUME_PATH:-/root/shared}"

# Logging helpers
log_info() {
    printf '%s %s\n' "[INFO]" "$*" >&2
}

log_warn() {
    printf '%s %s\n' "[WARNING]" "$*" >&2
}

log_warning() { log_warn "$@"; }

log_error() {
    printf '%s %s\n' "[ERROR]" "$*" >&2
}

log_success() {
    printf '%s %s\n' "[OK]" "$*" >&2
}

log_debug() {
    if [ "${DEBUG:-false}" = "true" ] || [ "${DEBUG:-}" = "1" ]; then
        printf '%s %s\n' "[DEBUG]" "$*" >&2
    fi
}

# Simple require_env - ensure variables are set and non-empty
require_env() {
    local missing=()
    for var in "$@"; do
        if [ -z "${!var:-}" ]; then
            missing+=("$var")
        fi
    done
    if [ "${#missing[@]}" -ne 0 ]; then
        log_error "Missing required environment variables: ${missing[*]}"
        return 1
    fi
    return 0
}

# Backward compatible alias
validate_required_vars() { require_env "$@"; }

# Basic YAML syntax validation using python -c if available or yq if present
validate_yaml_syntax() {
    local file="$1"
    if command -v yq >/dev/null 2>&1; then
        yq e '.' "$file" >/dev/null 2>&1 || return 1
        return 0
    elif command -v python3 >/dev/null 2>&1; then
        python3 -c "import sys, yaml; yaml.safe_load(open(sys.argv[1]));" "$file" >/dev/null 2>&1 || return 1
        return 0
    else
        log_warning "No YAML validator (yq/python) available; skipping strict YAML validation"
        return 0
    fi
}

# Robust library sourcing for airgap helpers
source_airgap_lib() {
    local candidates=(
        "${COMMON_LIB_DIR}/../pipeline/scripts/airgap/airgap_lib.sh"
        "${COMMON_LIB_DIR}/../../validation/pipeline/scripts/airgap/airgap_lib.sh"
        "${COMMON_LIB_DIR}/../../validation/pipeline/ci/airgap.groovy"
    )
    for p in "${candidates[@]}"; do
        if [ -f "$p" ]; then
            # shellcheck disable=SC1090
            source "$p" || return 1
            log_debug "Sourced airgap library: $p"
            return 0
        fi
    done
    log_warning "airgap library not found in candidates: ${candidates[*]}"
    return 1
}

# Initialize airgap environment - placeholder for repo-specific setup
initialize_airgap_environment() {
    log_info "Initializing airgap environment"
    # ensure QA_INFRA_WORK_PATH exists
    if [ -n "${QA_INFRA_WORK_PATH:-}" ] && [ -d "$QA_INFRA_WORK_PATH" ]; then
        log_debug "QA infra path exists: $QA_INFRA_WORK_PATH"
    else
        log_debug "QA_INFRA_WORK_PATH missing or not present; continuing"
    fi
    return 0
}

# Wait for confirmation (no-op in non-interactive CI)
wait_for_confirmation() {
    local prompt="${1:-Press Enter to continue...}"
    if [ -t 0 ]; then
        printf '%s' "$prompt"
        read -r _
    else
        log_debug "Non-interactive shell, skipping confirmation prompt"
    fi
}

# Simple parse_args implementation that fills PARSED_ARGS array
parse_args() {
    local usage="$1"
    shift || true
    # shellcheck disable=SC2034
    PARSED_ARGS=()
    local use_local="false"
    local no_s3="false"
    while [ $# -gt 0 ]; do
        case "$1" in
            -w | --workspace)
                PARSED_ARGS[0]="$2"
                shift 2
                ;;
            -v | --var-file)
                PARSED_ARGS[1]="$2"
                shift 2
                ;;
            -l | --local-path)
                use_local="true"
                shift
                ;;
            --no-s3-upload)
                no_s3="true"
                shift
                ;;
            --debug | -d)
                DEBUG=true
                shift
                ;;
            -h | --help)
                printf '%s\n' "$usage"
                exit 0
                ;;
            *)
                # ignore unknown, advance
                shift
                ;;
        esac
    done
    PARSED_ARGS[2]="${use_local}"
    PARSED_ARGS[3]="${no_s3}"
}

# Small helper to create temp files securely
mktemp_file() {
    local tmp
    tmp="$(mktemp -p /tmp "common.XXXXXX")" || return 1
    printf '%s' "$tmp"
}

# Trap and cleanup helpers
_COMMON_TMP_FILES=()
register_tmp_file() {
    _COMMON_TMP_FILES+=("$1")
}
common_cleanup() {
    for f in "${_COMMON_TMP_FILES[@]:-}"; do
        [ -f "$f" ] && shred -vfz -n 3 "$f" >/dev/null 2>&1 || rm -f "$f" >/dev/null 2>&1 || true
    done
}
trap common_cleanup EXIT

# Export essential functions for subshell use
export -f log_info log_error log_warning log_success log_debug require_env validate_required_vars validate_yaml_syntax source_airgap_lib initialize_airgap_environment wait_for_confirmation parse_args mktemp_file

# End of common.sh
