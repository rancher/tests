#!/bin/bash

# ========================================
# VALIDATION HELPER FUNCTIONS
# ========================================

# Validate RKE2 version format
validate_rke2_version() {
  local version="$1"
  if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\+rke2r[0-9]+$ ]]; then
    echo "ERROR: RKE2_VERSION '$version' does not match expected format (e.g., v1.28.8+rke2r1)"
    return 1
  fi
  return 0
}

# Validate Rancher version format
validate_rancher_version() {
  local version="$1"
  if [[ ! "$version" =~ ^(v[0-9]+\.[0-9]+(-head|\.[0-9]+)?|head)$ ]]; then
    echo "ERROR: RANCHER_VERSION '$version' does not match expected format (e.g., v2.10-head, v2.11.0, head)"
    return 1
  fi
  return 0
}

# Validate all pipeline parameters
validate_pipeline_parameters() {
  local errors=0

  # Required parameters
  if [ -z "$RKE2_VERSION" ]; then
    echo "ERROR: RKE2_VERSION parameter is required"
    ((errors++))
  else
    validate_rke2_version "$RKE2_VERSION" || ((errors++))
  fi

  if [ -z "$RANCHER_VERSION" ]; then
    echo "ERROR: RANCHER_VERSION parameter is required"
    ((errors++))
  else
    validate_rancher_version "$RANCHER_VERSION" || ((errors++))
  fi

  if [ -z "$RANCHER_TEST_REPO_URL" ]; then
    echo "ERROR: RANCHER_TEST_REPO_URL parameter is required"
    ((errors++))
  fi

  if [ -z "$QA_INFRA_REPO_URL" ]; then
    echo "ERROR: QA_INFRA_REPO_URL parameter is required"
    ((errors++))
  fi

  if [ $errors -gt 0 ]; then
    echo "Parameter validation failed with $errors error(s)"
    return 1
  fi

  echo "All parameters validated successfully"
  return 0
}

# Validate required environment variables
validate_required_variables() {
  local missing_vars=()

  for var_name in "$@"; do
    if [ -z "${!var_name}" ]; then
      missing_vars+=("$var_name")
    fi
  done

  if [ ${#missing_vars[@]} -gt 0 ]; then
    echo "ERROR: Missing required environment variables: ${missing_vars[*]}"
    return 1
  fi

  echo "All required variables validated successfully"
  return 0
}

# Validate sensitive data handling
validate_sensitive_data_handling() {
  local env_file="$1"
  local errors=0

  if [ ! -f "$env_file" ]; then
    echo "WARNING: Environment file not found: $env_file"
    return 0
  fi

  # Patterns that should never appear in env files
  local sensitive_patterns=(
    "AWS_SECRET_ACCESS_KEY="
    "AWS_ACCESS_KEY_ID="
    "AWS_SSH_PEM_KEY="
    "PRIVATE_REGISTRY_PASSWORD="
    "-----BEGIN"
    "-----END"
  )

  for pattern in "${sensitive_patterns[@]}"; do
    if grep -q "$pattern" "$env_file" && ! grep -q "${pattern} excluded" "$env_file"; then
      echo "ERROR: Sensitive pattern '$pattern' found in environment file"
      ((errors++))
    fi
  done

  # Verify withCredentials mention
  if ! grep -q "withCredentials" "$env_file"; then
    echo "WARNING: Environment file should mention withCredentials for security"
  fi

  if [ $errors -gt 0 ]; then
    echo "Sensitive data validation failed with $errors error(s)"
    return 1
  fi

  echo "Sensitive data handling validation passed"
  return 0
}

# Validate SSH key permissions
validate_ssh_key_permissions() {
  local key_path="$1"

  if [ ! -f "$key_path" ]; then
    echo "ERROR: SSH key file not found: $key_path"
    return 1
  fi

  local permissions
  permissions=$(stat -c '%a' "$key_path" 2>/dev/null || stat -f '%A' "$key_path" 2>/dev/null)

  if [ "$permissions" != "600" ]; then
    echo "ERROR: SSH key has insecure permissions: $permissions (should be 600)"
    return 1
  fi

  local dir_path
  dir_path=$(dirname "$key_path")
  local dir_permissions
  dir_permissions=$(stat -c '%a' "$dir_path" 2>/dev/null || stat -f '%A' "$dir_path" 2>/dev/null)

  if [ "$dir_permissions" != "700" ]; then
    echo "ERROR: SSH directory has insecure permissions: $dir_permissions (should be 700)"
    return 1
  fi

  echo "SSH key permissions validated successfully"
  return 0
}

# Main validation orchestration function
run_all_validations() {
  echo "=== Starting Comprehensive Validation ==="

  local overall_status=0

  # Run parameter validation
  echo "--- Validating Pipeline Parameters ---"
  validate_pipeline_parameters || overall_status=1

  # Run required variables validation if provided
  if [ $# -gt 0 ]; then
    echo "--- Validating Required Variables ---"
    validate_required_variables "$@" || overall_status=1
  fi

  # Run sensitive data validation if ENV_FILE is set
  if [ -n "$ENV_FILE" ]; then
    echo "--- Validating Sensitive Data Handling ---"
    validate_sensitive_data_handling "$ENV_FILE" || overall_status=1
  fi

  # Run SSH key validation if SSH_KEY_PATH is set
  if [ -n "$SSH_KEY_PATH" ]; then
    echo "--- Validating SSH Key Permissions ---"
    validate_ssh_key_permissions "$SSH_KEY_PATH" || overall_status=1
  fi

  if [ $overall_status -eq 0 ]; then
    echo "=== All Validations Passed ==="
  else
    echo "=== Validation Failed ==="
  fi

  return $overall_status
}
