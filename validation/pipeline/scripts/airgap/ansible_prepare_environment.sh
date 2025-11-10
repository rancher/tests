#!/bin/bash
set -Eeuo pipefail

# Ansible Environment Preparation Script
# Consolidated script that handles group_vars generation, inventory setup, and SSH key configuration
# Replaces: ansible_generate_group_vars.sh, ansible_setup_ssh_keys.sh, ansible_run_ssh_setup.sh

# airgap_lib will be sourced after SCRIPT_* constants are defined (see below)

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME
SCRIPT_DIR="$(dirname "$0")"
readonly SCRIPT_DIR
QA_INFRA_REPO_URL="${QA_INFRA_REPO_URL:-https://github.com/rancher/qa-infra-automation.git}"
readonly QA_INFRA_REPO_URL
QA_INFRA_REPO_BRANCH="${QA_INFRA_REPO_BRANCH:-main}"
readonly QA_INFRA_REPO_BRANCH
QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly QA_INFRA_CLONE_PATH

# Load the shared/common airgap library (try common.sh first, then legacy airgap_lib.sh)
# shellcheck disable=SC1090
if ! type log_info >/dev/null 2>&1; then
    lib_candidates=(
        "${SCRIPT_DIR}/../../../lib/common.sh" \
        "/root/go/src/github.com/rancher/tests/scripts/lib/common.sh" \
        "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/lib/common.sh" \
        "${SCRIPT_DIR}/airgap_lib.sh" \
        "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_lib.sh" \
        "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh" \
        "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    )

    for lib in "${lib_candidates[@]}"; do
        if [[ -f "$lib" ]]; then
            # shellcheck disable=SC1090
            source "$lib"
            if type log_info >/dev/null 2>&1; then
                log_info "Sourced common/airgap library from: $lib"
                break
            fi
        fi
    done

    if ! type log_info >/dev/null 2>&1; then
        echo "[ERROR] common/airgap library not found in expected locations: ${lib_candidates[*]}" >&2
        exit 1
    fi
fi
# Ensure generate_group_vars is available even if common.sh provided logging only
if ! type generate_group_vars >/dev/null 2>&1; then
    airgap_candidates=(
        "${SCRIPT_DIR}/airgap_lib.sh" \
        "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_lib.sh" \
        "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh" \
        "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    )
    for lib in "${airgap_candidates[@]}"; do
        [[ -f "$lib" ]] || continue
        # shellcheck disable=SC1090
        source "$lib"
        type generate_group_vars >/dev/null 2>&1 && break
    done
fi

# =============================================================================
# ANSIBLE ENVIRONMENT PREPARATION
# =============================================================================

prepare_ansible_environment() {
    log_info "Starting Ansible environment preparation"

    # Validate required variables
    validate_required_vars "ANSIBLE_VARIABLES"

    # Ensure Ansible directory structure exists
    setup_ansible_directories

    # Generate group_vars from ANSIBLE_VARIABLES
    generate_group_vars "/root/group_vars"

    # Setup SSH keys for Ansible
    setup_ssh_keys

    # Clone or update qa-infra-automation repository
    manage_qa_infra_repository

    # Setup inventory file
    setup_inventory_file

    # Copy group_vars to Ansible location
    copy_group_vars_to_ansible_location

    # Validate prepared environment
    validate_ansible_environment

    log_success "Ansible environment preparation completed"
}

# =============================================================================
# DIRECTORY SETUP
# =============================================================================

setup_ansible_directories() {
    log_info "Setting up Ansible directory structure"

    local directories=(
        "/root/ansible"
        "/root/ansible/rke2"
        "/root/ansible/rke2/airgap"
        "/root/ansible/rke2/airgap/inventory"
        "/root/ansible/rke2/airgap/group_vars"
        "/root/group_vars"
        "/root/.ssh"
    )

    for dir in "${directories[@]}"; do
        if mkdir -p "$dir" 2>/dev/null; then
            log_debug "Created directory: $dir"
        else
            log_warning "Could not create directory: $dir"
        fi
    done

    log_success "Ansible directory structure created"
}

# =============================================================================
# REPOSITORY MANAGEMENT
# =============================================================================

manage_qa_infra_repository() {
    log_info "Managing qa-infra-automation repository"

    if [[ ! -d "$QA_INFRA_CLONE_PATH" ]]; then
        log_info "Cloning qa-infra-automation repository..."
        if git clone -b "$QA_INFRA_REPO_BRANCH" "$QA_INFRA_REPO_URL" "$QA_INFRA_CLONE_PATH"; then
            log_success "Repository cloned successfully"
        else
            log_error "Failed to clone repository"
            return 1
        fi
    else
        log_info "Updating qa-infra-automation repository..."
        cd "$QA_INFRA_CLONE_PATH" || {
            log_error "Failed to change to repository directory"
            return 1
        }

        if git fetch origin && git checkout "$QA_INFRA_REPO_BRANCH" && git pull origin "$QA_INFRA_REPO_BRANCH"; then
            log_success "Repository updated successfully"
        else
            log_warning "Repository update failed, using existing version"
        fi
    fi

    # Verify required playbooks exist
    validate_qa_infra_structure
}

# =============================================================================
# STRUCTURE VALIDATION
# =============================================================================

validate_qa_infra_structure() {
    log_info "Validating qa-infra-automation repository structure"

    local required_dirs=(
        "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks"
        "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/inventory"
    )

    for dir in "${required_dirs[@]}"; do
        if [[ ! -d "$dir" ]]; then
            log_error "Required directory not found: $dir"
            return 1
        fi
    done

    # Check for key playbooks
    local playbooks_dir="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks"
    local key_playbooks=(
        "deploy/rke2-tarball-playbook.yml"
        "deploy/rancher-helm-deploy-playbook.yml"
    )

    log_info "Checking for required playbooks:"
    for playbook in "${key_playbooks[@]}"; do
        local playbook_path="$playbooks_dir/$playbook"
        if [[ -f "$playbook_path" ]]; then
            log_info "  [OK] Found: $playbook"
        else
            log_warning "  [FAIL] Missing: $playbook"
        fi
    done

    log_success "Repository structure validation completed"
}

# =============================================================================
# INVENTORY MANAGEMENT
# =============================================================================

setup_inventory_file() {
    log_info "Setting up Ansible inventory file"

    local inventory_file="/root/ansible/rke2/airgap/inventory.yml"
    local shared_inventory="$SHARED_VOLUME_PATH/ansible-inventory.yml"

    # Check if inventory file exists in shared volume (from Terraform)
    if [[ -f "$shared_inventory" && -s "$shared_inventory" ]]; then
        log_info "Found inventory file in shared volume, copying to Ansible location"
        cp "$shared_inventory" "$inventory_file"
        log_success "Inventory file copied: $shared_inventory -> $inventory_file"
    else
        log_error "Inventory file not found in shared volume: $shared_inventory"
        log_warning "This typically means Terraform deployment has not completed successfully"
        return 1
    fi

    # Validate inventory file structure
    validate_inventory_file "$inventory_file"
}

# =============================================================================
# INVENTORY VALIDATION
# =============================================================================

validate_inventory_file() {
    local inventory_file="$1"

    log_info "Validating Ansible inventory file: $inventory_file"

    if [[ ! -f "$inventory_file" ]]; then
        log_error "Inventory file not found: $inventory_file"
        return 1
    fi

    if [[ ! -s "$inventory_file" ]]; then
        log_error "Inventory file is empty: $inventory_file"
        return 1
    fi

    log_info "=== Inventory File Analysis ==="

    # Check for required groups
    local has_servers=false
    local has_agents=false

    if grep -q "rke2_servers:" "$inventory_file"; then
        has_servers=true
        local server_count
        server_count=$(grep -A 20 "rke2_servers:" "$inventory_file" | grep -c "rke2-server-")
        log_info "  [OK] rke2_servers group found ($server_count nodes)"
    else
        log_warning "  [FAIL] rke2_servers group not found"
    fi

    if grep -q "rke2_agents:" "$inventory_file"; then
        has_agents=true
        local agent_count
        agent_count=$(grep -A 20 "rke2_agents:" "$inventory_file" | grep -c "rke2-agent-")
        log_info "  [OK] rke2_agents group found ($agent_count nodes)"
    else
        log_warning "  [FAIL] rke2_agents group not found"
    fi

    # Check for legacy structure
    if grep -q "airgap_nodes:" "$inventory_file" && ! grep -q "rke2_servers:" "$inventory_file"; then
        log_warning "  [WARN] Using legacy inventory structure (airgap_nodes only)"
        log_warning "    This may cause all nodes to become control-plane nodes"
    fi

    # Total nodes count
    local total_nodes
    total_nodes=$(grep -E -c "rancher_node_[0-9]+" "$inventory_file" || true)
    if [[ -z "$total_nodes" ]]; then
        total_nodes="0"
    fi
    log_info "  Total RKE2 nodes: $total_nodes"

    # Validate structure
    if [[ $total_nodes -eq 0 ]]; then
        log_error "No RKE2 nodes found in inventory file"
        return 1
    fi

    if [[ "$has_servers" == "true" && "$has_agents" == "true" ]]; then
        log_success "Proper server/agent role separation detected"
    elif [[ $total_nodes -gt 1 ]]; then
        log_warning "Multiple nodes detected but no role separation"
        log_warning "This will likely result in all nodes becoming control-plane nodes"
    fi

    # Show inventory content (truncated if too large)
    local inventory_lines
    inventory_lines=$(wc -l <"$inventory_file")
    log_info "Inventory file has $inventory_lines lines"

    if [[ $inventory_lines -le 50 ]]; then
        log_debug "=== Full Inventory Content ==="
        cat "$inventory_file"
        log_debug "=== End Inventory Content ==="
    else
        log_debug "=== Inventory Content (truncated) ==="
        head -30 "$inventory_file"
        log_debug "... ($((inventory_lines - 60)) lines omitted) ..."
        tail -30 "$inventory_file"
        log_debug "=== End Inventory Content ==="
    fi

    log_success "Inventory file validation completed"
}

# =============================================================================
# GROUP_VARS MANAGEMENT
# =============================================================================

copy_group_vars_to_ansible_location() {
    log_info "Copying group_vars to Ansible location"

    local source_file="/root/group_vars/all.yml"
    local target_file="/root/ansible/rke2/airgap/group_vars/all.yml"

    if [[ ! -f "$source_file" ]]; then
        log_error "Source group_vars file not found: $source_file"
        return 1
    fi

    # Ensure target directory exists
    mkdir -p "$(dirname "$target_file")"

    # Copy the file
    cp "$source_file" "$target_file"
    log_success "group_vars copied to Ansible location: $target_file"

    # Ensure RKE2_VERSION is properly set
    ensure_rke2_version_in_group_vars "$target_file"

    # Validate the copied file
    validate_yaml_syntax "$target_file"
}

# =============================================================================
# RKE2 VERSION MANAGEMENT
# =============================================================================

ensure_rke2_version_in_group_vars() {
    local group_vars_file="$1"

    log_info "Ensuring RKE2_VERSION is set in group_vars"

    local rke2_version="${RKE2_VERSION:-v1.28.8+rke2r1}"

    # Check if rke2_version already exists in the file
    if grep -q "^rke2_version:" "$group_vars_file"; then
        # Replace existing line
        sed -i "s/^rke2_version:.*/rke2_version: \"$rke2_version\"/" "$group_vars_file"
        log_info "Updated existing rke2_version: $rke2_version"
    else
        # Add new line
        echo "rke2_version: \"$rke2_version\"" >>"$group_vars_file"
        log_info "Added rke2_version: $rke2_version"
    fi

    # Verify the variable is set
    if grep -q "^rke2_version:" "$group_vars_file"; then
        local set_version
        set_version=$(grep "^rke2_version:" "$group_vars_file" | cut -d'"' -f2)
        log_success "rke2_version confirmed in group_vars: $set_version"
    else
        log_error "Failed to set rke2_version in group_vars"
        return 1
    fi
}

# =============================================================================
# ENVIRONMENT VALIDATION
# =============================================================================

validate_ansible_environment() {
    log_info "Validating prepared Ansible environment"

    local validation_errors=0

    # Check required files
    local required_files=(
        "/root/group_vars/all.yml"
        "/root/ansible/rke2/airgap/group_vars/all.yml"
        "/root/ansible/rke2/airgap/inventory.yml"
        "/root/.ssh/authorized_keys"
    )

    log_info "Checking required files:"
    for file in "${required_files[@]}"; do
        if [[ -f "$file" ]]; then
            local file_size
            file_size=$(stat -c%s "$file" 2>/dev/null || echo 0)
            if [[ $file_size -gt 0 ]]; then
                log_info "  [OK] $file ($file_size bytes)"
            else
                log_error "  [FAIL] $file (empty)"
                ((validation_errors++))
            fi
        else
            log_error "  [FAIL] $file (missing)"
            ((validation_errors++))
        fi
    done

    # Check repository structure
    if [[ -d "$QA_INFRA_CLONE_PATH" ]]; then
        log_info "  [OK] qa-infra-automation repository exists"
    else
        log_error "  [FAIL] qa-infra-automation repository missing"
        ((validation_errors++))
    fi

    # Check required directories
    local required_dirs=(
        "/root/ansible/rke2/airgap"
        "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks"
    )

    log_info "Checking required directories:"
    for dir in "${required_dirs[@]}"; do
        if [[ -d "$dir" ]]; then
            log_info "  [OK] $dir"
        else
            log_error "  [FAIL] $dir"
            ((validation_errors++))
        fi
    done

    # Check environment variables
    local required_vars=("ANSIBLE_VARIABLES" "RKE2_VERSION")
    log_info "Checking environment variables:"
    for var in "${required_vars[@]}"; do
        if [[ -n "${!var}" ]]; then
            log_info "  [OK] $var is set"
        else
            log_warning "  [WARN] $var is not set"
        fi
    done

    # Summary
    if [[ $validation_errors -eq 0 ]]; then
        log_success "Ansible environment validation passed"
        return 0
    else
        log_error "Ansible environment validation failed with $validation_errors errors"
        return 1
    fi
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat <<EOF
Usage: $SCRIPT_NAME [OPTIONS]

Ansible Environment Preparation Script
This script prepares the Ansible environment for RKE2 and Rancher deployment by:
- Generating group_vars from ANSIBLE_VARIABLES
- Setting up SSH keys
- Cloning/updating qa-infra-automation repository
- Configuring inventory files
- Validating the prepared environment

OPTIONS:
    -h, --help                 Show this help message
    --debug                   Enable debug logging
    --skip-repo-update        Skip repository update (use existing)

ENVIRONMENT VARIABLES:
    ANSIBLE_VARIABLES           Ansible variables content or file path
    ANSIBLE_VARIABLES_FILE      Path to file containing ANSIBLE_VARIABLES
    RKE2_VERSION               RKE2 version to deploy
    QA_INFRA_REPO_URL          URL of qa-infra-automation repository
    QA_INFRA_REPO_BRANCH       Branch to checkout (default: main)
    AWS_SSH_PEM_KEY            SSH private key for node access
    DEBUG                      Enable debug logging (true/false)

EXAMPLES:
    # Prepare environment with default settings
    $SCRIPT_NAME

    # Prepare environment with debug logging
    DEBUG=true $SCRIPT_NAME --debug

    # Prepare environment without updating repository
    $SCRIPT_NAME --skip-repo-update

EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local skip_repo_update="false"

    while [[ $# -gt 0 ]]; do
        case $1 in
            --skip-repo-update)
                skip_repo_update="true"
                shift
                ;;
            --debug)
                export DEBUG="true"
                shift
                ;;
            -h | --help)
                show_help
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                show_help
                exit 1
                ;;
        esac
    done

    export SKIP_REPO_UPDATE="$skip_repo_update"

    log_info "Configuration:"
    log_info "  Skip repository update: $skip_repo_update"
    log_info "  Debug mode: ${DEBUG:-false}"
}

# =============================================================================
# MAIN SCRIPT EXECUTION
# =============================================================================

main() {
    log_info "=== Ansible Environment Preparation Started ==="
    log_info "Script: $SCRIPT_NAME"
    log_info "Timestamp: $(date)"
    log_info "Working directory: $(pwd)"

    # Parse command line arguments
    parse_arguments "$@"

    # Initialize the airgap environment
    initialize_airgap_environment

    # Validate required environment variables
    validate_required_vars "ANSIBLE_VARIABLES"

    # Wait for confirmation if in interactive mode
    wait_for_confirmation "Press Enter to start Ansible environment preparation..."

    # Run the preparation
    prepare_ansible_environment

    log_success "=== Ansible Environment Preparation Completed ==="
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"
