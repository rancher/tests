#!/bin/bash

# Setup script for Airgap RKE2 Jenkins Job
# This script helps configure the Jenkins job with proper credentials and parameters

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../../../" && pwd)"

echo "=== Airgap RKE2 Jenkins Job Setup ==="
echo "Project root: ${PROJECT_ROOT}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if required files exist
check_files() {
    print_status "Checking required files..."

    local files=(
        "validation/pipeline/Jenkinsfile.airgap.rke2"
        "validation/pipeline/configs/airgap-rke2-ansible-vars.yaml.template"
        "validation/pipeline/configs/airgap-rke2-terraform.tfvars.template"
        "validation/pipeline/docs/airgap-rke2-jenkins-job.md"
    )

    for file in "${files[@]}"; do
        if [[ -f "${PROJECT_ROOT}/${file}" ]]; then
            print_success "Found: ${file}"
        else
            print_error "Missing: ${file}"
            exit 1
        fi
    done
}

# Display Jenkins credentials requirements
show_credentials() {
    print_status "Required Jenkins Credentials:"
    echo ""
    echo "AWS Credentials:"
    echo "  - AWS_ACCESS_KEY_ID (String)"
    echo "  - AWS_SECRET_ACCESS_KEY (String)"
    echo "  - AWS_SSH_PEM_KEY (String - Base64 encoded)"
    echo "  - AWS_SSH_KEY_NAME (String)"
    echo ""
    echo "Registry Credentials:"
    echo "  - RANCHER_REGISTRY_USER_NAME (String)"
    echo "  - RANCHER_REGISTRY_PASSWORD (String)"
    echo "  - PRIVATE_REGISTRY_URL (String)"
    echo "  - PRIVATE_REGISTRY_USERNAME (String)"
    echo "  - PRIVATE_REGISTRY_PASSWORD (String)"
    echo ""
    echo "System Credentials:"
    echo "  - ADMIN_PASSWORD (String)"
    echo "  - USER_PASSWORD (String)"
    echo "  - RANCHER_SSH_KEY (String)"
    echo "  - BASTION_HOST (String - Optional)"
    echo ""
    echo "Optional:"
    echo "  - SLACK_WEBHOOK (String)"
    echo ""
}

# Display environment variables
show_environment_variables() {
    print_status "Required Environment Variables:"
    echo ""
    echo "Required:"
    echo "  - AWS_REGION (e.g., us-west-2)"
    echo "  - AWS_VPC (e.g., vpc-xxxxxxxxx)"
    echo "  - AWS_SUBNET_A (e.g., subnet-xxxxxxxxx)"
    echo "  - AWS_SUBNET_B (e.g., subnet-yyyyyyyyy)"
    echo "  - AWS_SUBNET_C (e.g., subnet-zzzzzzzzz)"
    echo "  - AWS_SECURITY_GROUPS (e.g., sg-xxxxxxxxx)"
    echo "  - AWS_AMI (e.g., ami-xxxxxxxxx)"
    echo "  - RKE2_VERSION (e.g., v1.28.8+rke2r1)"
    echo "  - RANCHER_VERSION (e.g., 2.8.3)"
    echo ""
    echo "Optional:"
    echo "  - TIMEOUT (default: 120m)"
    echo "  - CLEANUP_RESOURCES (default: true)"
    echo "  - QA_INFRA_REPO_BRANCH (default: main)"
    echo "  - QA_INFRA_REPO_URL (default: https://github.com/rancher/qa-infra-automation)"
    echo "  - RANCHER_REPO (default: https://github.com/rancher/rancher-tests)"
    echo "  - BRANCH (default: main)"
    echo ""
}

# Generate sample configurations
generate_sample_configs() {
    print_status "Generating sample configuration files..."

    local config_dir="${PROJECT_ROOT}/validation/pipeline/configs"

    # Generate sample Ansible vars
    if [[ ! -f "${config_dir}/airgap-rke2-ansible-vars.yaml.example" ]]; then
        cp "${config_dir}/airgap-rke2-ansible-vars.yaml.template" \
           "${config_dir}/airgap-rke2-ansible-vars.yaml.example"

        # Replace template variables with example values
        sed -i 's/\${RKE2_VERSION}/v1.28.8+rke2r1/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"
        sed -i 's/\${RANCHER_VERSION}/2.8.3/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"
        sed -i 's/\${PRIVATE_REGISTRY_URL}/registry.airgap.local:5000/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"
        sed -i 's/\${PRIVATE_REGISTRY_USERNAME}/admin/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"
        sed -i 's/\${PRIVATE_REGISTRY_PASSWORD}/password123/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"
        sed -i 's/\${BASTION_HOST}/10.0.1.100/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"
        sed -i 's/\${ADMIN_PASSWORD}/admin123/g' "${config_dir}/airgap-rke2-ansible-vars.yaml.example"

        print_success "Generated: airgap-rke2-ansible-vars.yaml.example"
    fi

    # Generate sample Terraform vars
    if [[ ! -f "${config_dir}/airgap-rke2-terraform.tfvars.example" ]]; then
        cp "${config_dir}/airgap-rke2-terraform.tfvars.template" \
           "${config_dir}/airgap-rke2-terraform.tfvars.example"

        # Replace template variables with example values
        sed -i 's/\${AWS_ACCESS_KEY_ID}/AKIAIOSFODNN7EXAMPLE/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_SECRET_ACCESS_KEY}/wJalrXUtnFEMI\/K7MDENG\/bPxRfiCYEXAMPLEKEY/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_REGION}/us-west-2/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_VPC}/vpc-12345678/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_SUBNET_A}/subnet-12345678/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_SUBNET_B}/subnet-87654321/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_SUBNET_C}/subnet-11223344/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_SECURITY_GROUPS}/sg-12345678/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_AMI}/ami-0abcdef1234567890/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"
        sed -i 's/\${AWS_SSH_KEY_NAME}/my-key-pair/g' "${config_dir}/airgap-rke2-terraform.tfvars.example"

        print_success "Generated: airgap-rke2-terraform.tfvars.example"
    fi
}

# Validate Jenkins CLI availability
check_jenkins_cli() {
    if command -v jenkins-cli &> /dev/null; then
        print_success "Jenkins CLI is available"
        return 0
    else
        print_warning "Jenkins CLI not found. Manual job creation required."
        return 1
    fi
}

# Display Jenkins job creation instructions
show_jenkins_instructions() {
    print_status "Jenkins Job Creation Instructions:"
    echo ""
    echo "1. Create a new Pipeline job in Jenkins"
    echo "2. Configure the following parameters:"
    echo "   - ANSIBLE_CONFIG (Text Parameter)"
    echo "   - TERRAFORM_CONFIG (Text Parameter)"
    echo "   - RKE2_VERSION (String Parameter, default: v1.28.8+rke2r1)"
    echo "   - RANCHER_VERSION (String Parameter, default: 2.8.3)"
    echo "   - CLEANUP_RESOURCES (Boolean Parameter, default: true)"
    echo "   - TIMEOUT (String Parameter, default: 120m)"
    echo ""
    echo "3. Set Pipeline Definition to 'Pipeline script from SCM'"
    echo "4. Configure SCM with your repository URL"
    echo "5. Set Script Path to: validation/pipeline/Jenkinsfile.airgap.rke2"
    echo ""
    echo "6. Configure the required credentials in Jenkins Credential Store"
    echo "7. Set up the environment variables in the job configuration"
    echo ""
}

# Main execution
main() {
    echo ""
    print_status "Starting Airgap RKE2 Jenkins Job Setup..."
    echo ""

    check_files
    echo ""

    show_credentials
    echo ""

    show_environment_variables
    echo ""

    generate_sample_configs
    echo ""

    if check_jenkins_cli; then
        print_status "Jenkins CLI detected. You can use it for automated job creation."
    fi
    echo ""

    show_jenkins_instructions
    echo ""

    print_success "Setup complete! Check the documentation at:"
    print_success "  ${PROJECT_ROOT}/validation/pipeline/docs/airgap-rke2-jenkins-job.md"
    echo ""

    print_status "Next steps:"
    echo "1. Review and customize the configuration templates"
    echo "2. Set up Jenkins credentials"
    echo "3. Create the Jenkins job"
    echo "4. Test the deployment"
    echo ""
}

# Run main function
main "$@"
