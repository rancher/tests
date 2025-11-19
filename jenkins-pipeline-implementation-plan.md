# Problem Statement

Create Jenkins pipelines to run Ansible playbooks and Tofu modules from qa-infra-automation repository for RKE2 airgap infrastructure setup and teardown.

# Overview of Current State

## Repository Structure

* **qa-infra-automation**: Contains Tofu modules at `tofu/aws/modules/airgap/` and Ansible playbooks at `ansible/rke2/airgap/playbooks/`
* **rancher-tests**: Target location for Jenkinsfiles at `validation/pipeline/`
* **qa-jenkins-library**: Shared library with functions for project checkout, container management, and credentials handling
* **jenkins-job-builder**: Defines two jobs `ansible-airgap-create-setup` and `ansible-airgap-delete-setup` at `qa-ansible-airgap-setup.yml`

## Infrastructure Components

The airgap Tofu module (`tofu/aws/modules/airgap/`) creates:

* Bastion host with public IP
* Optional registry instance
* Load balancers (external and internal)
* Airgap nodes (configurable groups, default: 3 rancher nodes)
* Route53 DNS records
* Auto-generated Ansible inventory at `ansible/rke2/airgap/inventory/inventory.yml`

Ansible playbooks deploy:

* RKE2 cluster using tarball method (no registry required)
* Optional: Rancher via Helm on the RKE2 cluster
* Registry configuration for private registries

## Job Definition Analysis

From `qa-ansible-airgap-setup.yml`:

* **Create job** (`ansible-airgap-create-setup`): References script path `validation/pipeline/Jenkinsfile.setup.airgap.rke2`
* **Delete job** (`ansible-airgap-delete-setup`): References script path `validation/pipeline/Jenkinsfile.destroy.airgap.rke2`
* Parameters include: RKE2_VERSION, RANCHER_VERSION, repository URLs/branches, DESTROY_ON_FAILURE flag, hostname prefix, registry credentials, S3 backend configuration, TERRAFORM_CONFIG, and ANSIBLE_VARIABLES
* Uses folder `rancher_qa`

## Existing Pipeline Patterns

From `Jenkinsfile.recurring` and `tfp` examples:

* Use `node {}` scripted pipeline syntax
* Clone both rancher-tests and qa-infra-automation repositories
* Write configuration files from Jenkins text parameters
* Use AWS credentials, SSH keys via Jenkins credential store
* Build Docker containers for test execution
* For Terraform/Tofu: use workspaces and S3 backend for state management

# Proposed Changes

## Create Lean Dockerfile for Infrastructure Operations

### Dockerfile.infra

Location: `validation/pipeline/Dockerfile.infra`

A minimal container image containing only:

* Base: Alpine Linux (smallest footprint)
* OpenTofu/Tofu binary
* Ansible and required collections
* Python3 and pip (for Ansible)
* OpenSSH client (for Ansible remote execution)
* AWS CLI (for S3 backend operations)
* Basic utilities: bash, curl, git
* No Go toolchain, no test frameworks, no build tools

Build arguments:

* `TOFU_VERSION` - OpenTofu version to install
* `ANSIBLE_VERSION` - Ansible version to install

The image will be built once and reused across pipeline runs for consistency and speed.

## Create Two Jenkinsfiles

### 1. Jenkinsfile.setup.airgap.rke2

Location: `validation/pipeline/Jenkinsfile.setup.airgap.rke2`

Stages:

1. **Checkout**: Clone qa-infra-automation repository (rancher-tests already available via SCM)
2. **Configure Tofu Variables**: Write TERRAFORM_CONFIG parameter to `terraform.tfvars` file in airgap module directory
3. **Initialize Tofu Backend**: Run `tofu init` with S3 backend configuration from parameters
4. **Create/Select Workspace**: Create new workspace with unique name (e.g., `jenkins_airgap_<BUILD_NUMBER>_<timestamp>`)
5. **Apply Tofu**: Run `tofu apply -auto-approve` to create infrastructure
6. **Configure Ansible Variables**: Write ANSIBLE_VARIABLES parameter to `ansible/rke2/airgap/inventory/group_vars/all.yml`
7. **Run Ansible Setup Playbooks**: 
    * Execute `playbooks/setup/setup-ssh-keys.yml`
    * Execute `playbooks/deploy/rke2-tarball-playbook.yml` with target group
    * Conditionally run `playbooks/deploy/rancher-helm-deploy-playbook.yml` if deploy_rancher is enabled
8. **Post-Success**: Output infrastructure details, workspace name, and access information
9. **Post-Failure**: Optionally run destroy if DESTROY_ON_FAILURE is true

### 2. Jenkinsfile.destroy.airgap.rke2

Location: `validation/pipeline/Jenkinsfile.destroy.airgap.rke2`

Stages:

1. **Checkout**: Clone qa-infra-automation repository
2. **Initialize Tofu Backend**: Run `tofu init` with S3 backend configuration
3. **Select Workspace**: Select workspace specified by TARGET_WORKSPACE parameter
4. **Destroy Infrastructure**: Run `tofu destroy -auto-approve`
5. **Delete Workspace**: Delete the Tofu workspace
6. **Cleanup**: Remove any local artifacts

## Implementation Details

### Credentials Required

* AWS_ACCESS_KEY_ID
* AWS_SECRET_ACCESS_KEY  
* AWS_SSH_PEM_KEY (base64 encoded)
* AWS_SSH_PEM_KEY_NAME
* PRIVATE_REGISTRY_USERNAME (optional)
* PRIVATE_REGISTRY_PASSWORD (optional)

### Tofu Backend Configuration

Use S3 backend with parameters:

* S3_BUCKET_NAME
* S3_BUCKET_REGION  
* S3_KEY_PREFIX

### Workspace Naming

Generate workspace names like: `jenkins_airgap_ansible_workspace_<BUILD_NUMBER>`

### File Generation

* Write SSH key from base64 credential to `.ssh/` directory
* Parse and write TERRAFORM_CONFIG to `terraform.tfvars`
* Parse and write ANSIBLE_VARIABLES to `group_vars/all.yml`
* Variable substitution for AWS credentials, hostname prefix, versions

### Ansible Execution

Run ansible-playbook commands directly on Jenkins node:

```bash
ansible-playbook -i inventory/inventory.yml playbooks/deploy/rke2-tarball-playbook.yml
```

### Error Handling

* Capture Tofu/Ansible output
* On failure in setup pipeline: optionally destroy infrastructure if DESTROY_ON_FAILURE=true
* Ensure workspace name is saved for later cleanup

### Shared Library Usage

Create new functions in `qa-jenkins-library` to abstract common operations:

#### New Functions to Add:

1. **`tofu.groovy`** - Tofu/Terraform operations:
    * `initBackend(Map config)` - Initialize backend with S3 configuration
    * `createWorkspace(String name)` - Create and select workspace
    * `selectWorkspace(String name)` - Select existing workspace
    * `apply(Map config)` - Run tofu apply with options
    * `destroy(Map config)` - Run tofu destroy with options
    * `deleteWorkspace(String name)` - Delete workspace
    * `getOutputs()` - Retrieve tofu outputs as map

2. **`ansible.groovy`** - Ansible operations:
    * `runPlaybook(Map config)` - Execute ansible-playbook with inventory and options
    * `writeInventoryVars(String path, String content)` - Write variables to group_vars

3. **`infrastructure.groovy`** - High-level infrastructure helpers:
    * `writeConfig(String path, String content, Map substitutions)` - Write config with variable substitution
    * `writeSshKey(String keyContent, String keyName, String dir)` - Decode and write SSH key
    * `generateWorkspaceName(String prefix)` - Generate unique workspace name

Existing library functions to use:

* `property.useWithProperties()` for credentials wrapping
* `project.checkout()` for repository checkout

## Output Artifacts

Setup pipeline should output/save:

* Workspace name (for destroy job)
* Bastion host public DNS
* Rancher hostname (external LB)
* Internal LB hostname
* Generated inventory file location
