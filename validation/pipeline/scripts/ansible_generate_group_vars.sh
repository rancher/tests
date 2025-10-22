#!/bin/bash
set -e

# Ansible Group Variables Generation Script
# This script generates the Ansible group_vars/all.yml file from the ANSIBLE_VARIABLES environment variable
# Uses the airgap library for the actual generation

# =============================================================================
# CONSTANTS
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly OUTPUT_DIR="${ANSIBLE_OUTPUT_DIR:-/root/ansible/rke2/airgap/group_vars}"

# =============================================================================
# LOGGING FUNCTIONS
# =============================================================================

log_info() { echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_prerequisites() {
  # If logging helper already exists, assume airgap library is loaded
  if type log_info >/dev/null 2>&1; then
    [[ -n "${ANSIBLE_VARIABLES:-}" ]] || { log_error "ANSIBLE_VARIABLES not set"; exit 1; }
    return 0
  fi

  local lib_candidates=(
    "${SCRIPT_DIR}/airgap_lib.sh"
    "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
    "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
  )

  for lib in "${lib_candidates[@]}"; do
    if [[ -f "$lib" ]]; then
      # shellcheck disable=SC1090
      source "$lib"
      log_info "Sourced airgap library from: $lib"
      break
    fi
  done

  if ! type log_info >/dev/null 2>&1; then
    log_error "airgap_lib.sh not found in expected locations: ${lib_candidates[*]}"
    exit 1
  fi

  [[ -n "${ANSIBLE_VARIABLES:-}" ]] || { log_error "ANSIBLE_VARIABLES not set"; exit 1; }
}

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

# Load the airgap library (only if present at known absolute path)
# shellcheck disable=SC1090
if [[ -f "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh" ]]; then
  source "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
fi

# =============================================================================
# MAIN FUNCTION
# =============================================================================

main() {
  log_info "Starting group variables generation with $SCRIPT_NAME"

  # Validate prerequisites
  validate_prerequisites

  # Validate required environment variables
  validate_required_vars "ANSIBLE_VARIABLES"

  log_info "Configuration:"
  log_info "  Output directory: $OUTPUT_DIR"
  log_info "  ANSIBLE_VARIABLES size: ${#ANSIBLE_VARIABLES} bytes"

  # Generate the group_vars using the airgap library function
  if generate_group_vars "$OUTPUT_DIR"; then
    log_info "=== Group Variables Generation Summary ==="
    log_info "Generated files:"
    if [[ -f "$OUTPUT_DIR/all.yml" ]]; then
      log_info "  - $OUTPUT_DIR/all.yml ($(wc -l < "$OUTPUT_DIR/all.yml") lines)"
    fi

    # Show first few lines of generated content
    if [[ -f "$OUTPUT_DIR/all.yml" ]]; then
      log_info "=== Generated Content Preview ==="
      head -10 "$OUTPUT_DIR/all.yml"
      log_info "=== End Preview ==="
    fi

    log_info "Group variables generation completed successfully"
  else
    log_error "Failed to generate group variables"
    exit 1
  fi
}

# Execute main function
main "$@"