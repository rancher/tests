# Airgap RKE2 Jenkins Job Documentation

## Overview

This documentation covers two Jenkins pipelines for airgap RKE2 deployments:

1. **Deployment Pipeline** (`Jenkinsfile.airgap.rke2.improved`): Automates the deployment of RKE2 clusters in airgap environments using Ansible playbooks from the [rancher/qa-infra-automation](https://github.com/rancher/qa-infra-automation) repository. Provisions infrastructure with OpenTofu, deploys RKE2 in airgap mode, and installs Rancher on the cluster.

2. **Destruction Pipeline** (`Jenkinsfile.destroy.airgap.rke2`): Provides automated infrastructure cleanup by retrieving Terraform state from S3 backend and performing controlled destruction of all resources.

## Table of Contents

- [Technology Stack](#technology-stack)
- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Job Configuration](#job-configuration)
- [Deployment Process](#deployment-process)
- [Infrastructure Destruction Pipeline](#infrastructure-destruction-pipeline)
  - [S3 Backend State Management](#s3-backend-state-management)
  - [Destruction Pipeline Stages](#destruction-pipeline-stages)
  - [Using the Destruction Pipeline](#using-the-destruction-pipeline)
- [Airgap-Specific Considerations](#airgap-specific-considerations)
- [Troubleshooting](#troubleshooting)
- [Monitoring and Alerts](#monitoring-and-alerts)
- [Best Practices](#best-practices)
- [Support](#support)
- [Pipeline Files](#pipeline-files)
- [References](#references)

## Technology Stack

- **Infrastructure as Code**: OpenTofu (Terraform-compatible)
- **Configuration Management**: Ansible (from qa-infra-automation repository)
- **State Management**: AWS S3 backend for Terraform/OpenTofu state
- **Container Runtime**: Docker for pipeline execution
- **Kubernetes Distribution**: RKE2
- **Platform Management**: Rancher

## Architecture

The airgap RKE2 deployment consists of:

1. **Bastion Host**: Jump server with internet access for initial setup
2. **Private Registry**: Container registry for airgap images
3. **RKE2 Server Nodes**: Control plane nodes (typically 3 for HA)
4. **RKE2 Agent Nodes**: Worker nodes (configurable count)
5. **Load Balancer**: Network load balancer for API server access

## Prerequisites

### Jenkins Credentials

The following credentials must be configured in Jenkins:

#### AWS Credentials
- `AWS_ACCESS_KEY_ID`: AWS access key for infrastructure provisioning
- `AWS_SECRET_ACCESS_KEY`: AWS secret key for infrastructure provisioning
- `AWS_SSH_PEM_KEY`: Base64-encoded SSH private key for EC2 instances
- `AWS_SSH_KEY_NAME`: Name of the SSH key pair in AWS

#### Registry Credentials
- `RANCHER_REGISTRY_USER_NAME`: Docker Hub username for pulling images
- `RANCHER_REGISTRY_PASSWORD`: Docker Hub password/token
- `PRIVATE_REGISTRY_URL`: URL of the private registry (e.g., `registry.airgap.local:5000`)
- `PRIVATE_REGISTRY_USERNAME`: Username for private registry
- `PRIVATE_REGISTRY_PASSWORD`: Password for private registry

#### System Credentials
- `ADMIN_PASSWORD`: Admin password for Rancher
- `USER_PASSWORD`: Standard user password
- `RANCHER_SSH_KEY`: SSH key for Rancher operations
- `BASTION_HOST`: Bastion host IP or hostname (if using existing bastion)

#### Optional Credentials
- `SLACK_WEBHOOK`: Slack webhook URL for notifications

### Environment Variables

The following environment variables should be configured in the Jenkins job:

#### Required Variables
```bash
AWS_REGION=us-west-2
AWS_VPC=vpc-xxxxxxxxx
AWS_SUBNET_A=subnet-xxxxxxxxx
AWS_SUBNET_B=subnet-yyyyyyyyy
AWS_SUBNET_C=subnet-zzzzzzzzz
AWS_SECURITY_GROUPS=sg-xxxxxxxxx
AWS_AMI=ami-xxxxxxxxx  # Ubuntu 20.04 LTS
RKE2_VERSION=v1.28.8+rke2r1
RANCHER_VERSION=2.8.3
```

#### Optional Variables
```bash
TIMEOUT=120m
CLEANUP_RESOURCES=true
QA_INFRA_REPO_BRANCH=main
QA_INFRA_REPO_URL=https://github.com/rancher/qa-infra-automation
RANCHER_REPO=https://github.com/rancher/rancher-tests
BRANCH=main
```

## Job Configuration

### Pipeline Parameters

The job accepts the following parameters:

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `ANSIBLE_CONFIG` | Text | - | Ansible variables configuration (YAML) |
| `TERRAFORM_CONFIG` | Text | - | Terraform variables configuration |
| `RKE2_VERSION` | String | `v1.28.8+rke2r1` | RKE2 version to deploy |
| `RANCHER_VERSION` | String | `2.8.3` | Rancher version to deploy |
| `CLEANUP_RESOURCES` | Boolean | `true` | Whether to cleanup resources after deployment |
| `TIMEOUT` | String | `120m` | Job timeout duration |

### Configuration Templates

Use the provided templates to configure your deployment:

- [`airgap-rke2-ansible-vars.yaml.template`](../configs/airgap-rke2-ansible-vars.yaml.template): Ansible variables template
- [`airgap-rke2-terraform.tfvars.template`](../configs/airgap-rke2-terraform.tfvars.template): Terraform variables template

## Deployment Process

### Stage 1: Checkout Repositories
- Clones the rancher/tests repository
- Clones the rancher/qa-infra-automation repository

### Stage 2: Configure Environment
- Processes configuration templates with environment variables
- Sets up SSH keys and permissions
- Builds Docker container for deployment

### Stage 3: Setup Airgap Infrastructure
- Initializes Terraform workspace
- Provisions AWS infrastructure:
  - VPC and networking components
  - Bastion host
  - Private registry server
  - RKE2 server and agent nodes
  - Load balancer

### Stage 4: Deploy RKE2 Airgap Cluster
- Configures private registry with required images
- Deploys RKE2 server nodes in airgap mode
- Joins agent nodes to the cluster
- Configures cluster networking and storage

### Stage 5: Deploy Rancher on Airgap Cluster
- Installs cert-manager
- Deploys Rancher using Helm
- Configures Rancher for airgap operation

### Stage 6: Validate Airgap Deployment
- Verifies cluster health
- Checks node status
- Validates pod deployments
- Tests Rancher accessibility

### Stage 7: Cleanup Resources (Optional)
- Destroys Terraform infrastructure if `CLEANUP_RESOURCES=true`
- Preserves resources for manual testing if cleanup is disabled

### Stage 8: Archive Results and Cleanup Containers
- Archives kubeconfig and Terraform state
- Cleans up Docker containers and volumes

## Infrastructure Destruction Pipeline

### Overview

The destruction pipeline (`Jenkinsfile.destroy.airgap.rke2`) provides automated infrastructure cleanup for environments deployed by the main airgap RKE2 pipeline. It retrieves Terraform state from S3 backend and performs controlled destruction of all resources.

### Prerequisites for Destruction

#### Required Parameters

| Parameter | Description | Example |
|-----------|-------------|---------|
| `TARGET_WORKSPACE` | Terraform workspace to destroy | `jenkins_airgap_ansible_workspace_123` |
| `S3_BUCKET_NAME` | S3 bucket storing Terraform state | `jenkins-terraform-state-storage` |
| `S3_KEY_PREFIX` | S3 key prefix for state files | `jenkins-airgap-rke2/terraform.tfstate` |
| `S3_REGION` | AWS region for S3 bucket | `us-east-2` |

#### Optional Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `RANCHER_TEST_REPO_URL` | `https://github.com/rancher/tests` | Repository URL |
| `RANCHER_TEST_REPO_BRANCH` | `main` | Repository branch |
| `QA_INFRA_REPO_URL` | `https://github.com/rancher/qa-infra-automation` | QA infra repo URL |
| `QA_INFRA_REPO_BRANCH` | `main` | QA infra repo branch |

### S3 Backend State Management

The destruction pipeline uses S3 backend for Terraform state management:

#### State Storage Structure
```
s3://jenkins-terraform-state-storage/
├── jenkins-airgap-rke2/terraform.tfstate    # Main state file
└── env:/jenkins_airgap_ansible_workspace_123/  # Workspace-specific configs
    └── cluster.tfvars                         # Terraform variables
```

#### State Retrieval Process
1. Downloads `cluster.tfvars` from S3 workspace directory
2. Generates `backend.tf` with S3 backend configuration
3. Initializes OpenTofu with remote state
4. Selects target workspace
5. Executes destruction plan

### Destruction Pipeline Stages

#### Stage 1: Initialize Pipeline
- Validates required parameters (`TARGET_WORKSPACE`, repository URLs)
- Cleans Jenkins workspace
- Logs pipeline configuration

#### Stage 2: Checkout Repositories
- Clones `rancher/tests` repository
- Clones `rancher/qa-infra-automation` repository
- Uses shallow cloning for performance

#### Stage 3: Configure Environment
- Generates environment file (`.env`) with AWS credentials and S3 configuration
- Sets up SSH keys if provided
- Builds Docker image for OpenTofu operations
- Creates shared volume for artifact persistence

#### Stage 4: Infrastructure Destruction Operations

**Configuration Download:**
- Downloads `cluster.tfvars` from S3 workspace directory
- Validates file retrieval and content

**OpenTofu Configuration:**
- Generates `backend.tf` with S3 backend configuration
- Copies backend configuration to container
- Validates backend connectivity

**Pre-flight Checks:**
- Validates infrastructure prerequisites using `tofu_validate_prerequisites.sh`
- Verifies required environment variables
- Checks workspace existence

**Destruction Execution:**
- Initializes OpenTofu with S3 backend
- Selects target workspace
- Runs `tofu destroy` with auto-approval
- Deletes workspace after successful destruction

**Post-destruction Validation:**
- Validates infrastructure state using `destroy_validate_state.sh`
- Confirms all resources are destroyed
- Archives destruction summary

#### Stage 5: S3 Cleanup (Success Only)

After successful destruction, the pipeline cleans up S3 resources:

**Workspace Directory Cleanup:**
- Deletes `env:/${TF_WORKSPACE}/` directory containing configuration files
- Removes `cluster.tfvars` and other workspace-specific files

**Terraform State Cleanup:**
- Deletes the Terraform state file at `${S3_KEY_PREFIX}`
- Verifies deletion with AWS CLI

**Cleanup Process:**
```bash
# Workspace directory deletion
aws s3 rm "s3://${S3_BUCKET_NAME}/env:/${TF_WORKSPACE}/" --recursive --region "${S3_REGION}"

# Terraform state deletion
aws s3 rm "s3://${S3_BUCKET_NAME}/${S3_KEY_PREFIX}" --region "${S3_REGION}"
```

### Destruction Scripts

The following scripts are used during destruction:

| Script | Purpose | Location |
|--------|---------|----------|
| `destroy_download_config.sh` | Download configuration from S3 | `validation/pipeline/scripts/` |
| `tofu_validate_prerequisites.sh` | Validate infrastructure prerequisites | `validation/pipeline/scripts/` |
| `tofu_initialize.sh` | Initialize OpenTofu with S3 backend | `validation/pipeline/scripts/` |
| `destroy_execute.sh` | Execute infrastructure destruction | `validation/pipeline/scripts/` |
| `tofu_delete_workspace.sh` | Delete OpenTofu workspace | `validation/pipeline/scripts/` |
| `destroy_validate_state.sh` | Validate post-destruction state | `validation/pipeline/scripts/` |

### Container Execution Pattern

Destruction operations run inside Docker containers with:

**Volume Mounts:**
- Shared volume: `/root` - Persists artifacts between containers
- Script mount: `/tmp/script.sh` - Temporarily mounted scripts

**Environment Variables:**
- Passed via `.env` file AND direct `-e` flags (fallback mechanism)
- Critical vars: `TF_WORKSPACE`, `S3_BUCKET_NAME`, `S3_KEY_PREFIX`, `AWS_REGION`

**Container Lifecycle:**
1. Create container with environment and volumes
2. Execute destruction script
3. Copy artifacts to shared volume
4. Remove container
5. Extract artifacts from shared volume to Jenkins workspace

### Archived Artifacts

The destruction pipeline archives:

- `destruction-plan.txt` - OpenTofu destruction plan
- `destruction-summary.json` - Destruction summary and results
- `destruction-logs.txt` - Complete destruction logs
- `workspace-list.txt` - OpenTofu workspace list (on failure)
- `remaining-resources.txt` - Any remaining resources (on failure)

### Failure Handling

**Timeout Handling:**
- Default timeout: 30 minutes
- On timeout: Archives failure artifacts and logs error

**Partial Destruction:**
- Lists remaining resources in state
- Archives workspace information
- Provides manual cleanup guidance

**State File Issues:**
- Validates S3 bucket access before operations
- Checks workspace existence
- Verifies backend configuration

### Using the Destruction Pipeline

#### Automated Destruction (Main Pipeline)

When `CLEANUP_RESOURCES=true` in the main pipeline, destruction is automatic.

#### Manual Destruction (Standalone)

To destroy a specific deployment:

1. **Find the workspace name** from the main pipeline logs or S3:
   ```bash
   aws s3 ls s3://jenkins-terraform-state-storage/ --recursive | grep env:
   ```

2. **Trigger the destruction pipeline** with parameters:
   - `TARGET_WORKSPACE`: e.g., `jenkins_airgap_ansible_workspace_123`
   - `S3_BUCKET_NAME`: `jenkins-terraform-state-storage`
   - `S3_KEY_PREFIX`: `jenkins-airgap-rke2/terraform.tfstate`
   - `S3_REGION`: `us-east-2`

3. **Monitor the destruction** via Jenkins console output

4. **Verify cleanup** by checking:
   - AWS Console for remaining resources
   - S3 bucket for deleted workspace directory
   - Terraform state file deletion

### Destruction Pipeline Best Practices

**Before Running:**
- Verify the correct workspace name
- Confirm S3 bucket access permissions
- Check for any running workloads that need preservation

**During Execution:**
- Monitor console logs for errors
- Watch for timeout warnings
- Verify each stage completes successfully

**After Completion:**
- Review archived artifacts
- Confirm S3 cleanup completion
- Verify AWS resources are destroyed via AWS Console

**Troubleshooting Destruction:**
- If destruction fails, check `remaining-resources.txt` artifact
- Review workspace state using AWS CLI
- Use manual cleanup if automated destruction is blocked

## Airgap-Specific Considerations

### Image Management
- All container images must be pre-loaded into the private registry
- Registry mirrors are configured for common registries (docker.io, quay.io, etc.)
- RKE2 images are downloaded and imported during deployment

### Network Isolation
- Agent nodes have no internet access
- All communication goes through the bastion host
- Private registry serves all container images

### Security
- SSH keys are managed securely through Jenkins credentials
- Registry credentials are encrypted
- Network security groups restrict access

## Troubleshooting

### Common Issues

#### Infrastructure Provisioning Failures
```bash
# Check Terraform logs in Jenkins console
# Verify AWS credentials and permissions
# Ensure VPC and subnet configurations are correct
```

#### RKE2 Deployment Failures
```bash
# Check Ansible playbook logs
# Verify SSH connectivity to nodes
# Ensure private registry is accessible
# Check RKE2 version compatibility
```

#### Rancher Deployment Failures
```bash
# Verify cluster is healthy before Rancher installation
# Check cert-manager deployment
# Ensure Helm is properly configured
# Verify registry mirrors are working
```

#### Infrastructure Destruction Failures

**S3 State Download Failures:**
```bash
# Verify S3 bucket exists and credentials are valid
aws s3 ls s3://jenkins-terraform-state-storage/

# Check workspace directory exists
aws s3 ls s3://jenkins-terraform-state-storage/env:/${WORKSPACE_NAME}/

# Manually download configuration
aws s3 cp s3://jenkins-terraform-state-storage/env:/${WORKSPACE_NAME}/cluster.tfvars ./
```

**OpenTofu Workspace Issues:**
```bash
# List available workspaces
tofu workspace list

# If workspace doesn't exist, check state file
aws s3 ls s3://jenkins-terraform-state-storage/jenkins-airgap-rke2/

# Reinitialize backend
tofu init -reconfigure
```

**Partial Destruction Failures:**
```bash
# Check remaining resources in state
tofu state list

# Show specific resource details
tofu state show <resource_name>

# Force remove stuck resources from state (use carefully)
tofu state rm <resource_name>

# Manually delete AWS resources then refresh state
tofu refresh
```

**S3 Cleanup Failures:**
```bash
# Verify AWS CLI credentials
aws sts get-caller-identity

# List workspace contents before deletion
aws s3 ls s3://jenkins-terraform-state-storage/env:/${WORKSPACE_NAME}/ --recursive

# Manual S3 cleanup if automated fails
aws s3 rm s3://jenkins-terraform-state-storage/env:/${WORKSPACE_NAME}/ --recursive
aws s3 rm s3://jenkins-terraform-state-storage/jenkins-airgap-rke2/terraform.tfstate
```

**Container/Volume Issues:**
```bash
# Check if Docker volume exists
docker volume ls | grep DestroySharedVolume

# Inspect volume contents
docker run --rm -v <volume_name>:/data alpine ls -la /data

# Force remove stuck containers
docker ps -a | grep destroy | awk '{print $1}' | xargs docker rm -f

# Clean up Docker resources
docker system prune -af --volumes
```

### Log Locations
- Jenkins console output contains all deployment logs
- Kubeconfig is archived as a build artifact
- Terraform state is archived for infrastructure debugging

### Manual Cleanup

#### Option 1: Automated Destruction Pipeline (Recommended)

Use the destruction pipeline (`Jenkinsfile.destroy.airgap.rke2`) for safe, automated cleanup:

1. **Find the workspace name** from deployment logs or S3:
   ```bash
   aws s3 ls s3://jenkins-terraform-state-storage/ --recursive | grep env:
   ```

2. **Trigger destruction pipeline** with parameters:
   - `TARGET_WORKSPACE`: Workspace name from deployment
   - `S3_BUCKET_NAME`: S3 bucket with Terraform state
   - `S3_KEY_PREFIX`: State file key prefix
   - `S3_REGION`: AWS region

3. **Monitor execution** and verify cleanup completion

See [Infrastructure Destruction Pipeline](#infrastructure-destruction-pipeline) section for details.

#### Option 2: Manual Destruction (Last Resort)

If the automated destruction pipeline fails, manually destroy resources:

```bash
# 1. Download configuration from S3
aws s3 cp s3://jenkins-terraform-state-storage/env:/${WORKSPACE_NAME}/cluster.tfvars ./cluster.tfvars

# 2. SSH to bastion host (if available)
ssh -i /path/to/key ubuntu@<bastion-ip>

# 3. Navigate to OpenTofu directory
cd /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap

# 4. Initialize with S3 backend
cat > backend.tf <<EOF
terraform {
  backend "s3" {
    bucket = "jenkins-terraform-state-storage"
    key    = "jenkins-airgap-rke2/terraform.tfstate"
    region = "us-east-2"
  }
}
EOF

tofu init

# 5. Select workspace and destroy
tofu workspace select ${WORKSPACE_NAME}
tofu destroy -auto-approve -var-file=cluster.tfvars

# 6. Delete workspace
tofu workspace select default
tofu workspace delete ${WORKSPACE_NAME}

# 7. Clean up S3 (from local machine)
aws s3 rm s3://jenkins-terraform-state-storage/env:/${WORKSPACE_NAME}/ --recursive
aws s3 rm s3://jenkins-terraform-state-storage/jenkins-airgap-rke2/terraform.tfstate
```

#### Option 3: Emergency AWS Console Cleanup

If OpenTofu/Terraform is unavailable:

1. **Identify resources** by tags or naming convention
2. **Delete in order**:
   - EC2 instances (RKE2 nodes, bastion, registry)
   - Load balancers
   - Security groups
   - Network interfaces
   - EBS volumes
   - Elastic IPs
3. **Verify** no orphaned resources remain
4. **Clean up S3** state files manually

## Monitoring and Alerts

### Success Indicators
- All pipeline stages complete successfully
- Cluster nodes are in Ready state
- Rancher UI is accessible
- All system pods are running

### Failure Indicators

**Deployment Pipeline:**
- OpenTofu/Terraform apply failures
- Ansible playbook errors
- Cluster validation failures
- Rancher deployment issues

**Destruction Pipeline:**
- S3 state download failures
- OpenTofu workspace selection errors
- Partial infrastructure destruction
- S3 cleanup failures
- Remaining resources in state file

### Notifications
Configure Slack webhook for deployment notifications:
- Success/failure status
- Deployment duration
- Resource cleanup status

## Best Practices

### Resource Management
- Always enable resource cleanup for CI/CD pipelines
- Use spot instances for cost optimization in testing
- Monitor AWS costs and set billing alerts

### Security
- Rotate SSH keys regularly
- Use least-privilege IAM policies
- Enable VPC flow logs for network monitoring
- Regularly update base AMIs

### Performance
- Use appropriate instance types for workload
- Enable enhanced networking for better performance
- Consider placement groups for high-performance computing

### Maintenance
- Keep RKE2 and Rancher versions updated
- Regularly update Ansible playbooks
- Monitor for security vulnerabilities
- Test disaster recovery procedures

## Support

For issues and questions:
- Check Jenkins console logs first
- Review Ansible playbook documentation in qa-infra-automation repo
- Consult RKE2 and Rancher documentation
- Contact the QA infrastructure team

## Pipeline Files

- **Deployment Pipeline**: `validation/pipeline/Jenkinsfile.airgap.rke2.improved`
- **Destruction Pipeline**: `validation/pipeline/Jenkinsfile.destroy.airgap.rke2`
- **Deployment Scripts**: `validation/pipeline/scripts/ansible_*.sh`, `validation/pipeline/scripts/tofu_*.sh`
- **Destruction Scripts**: `validation/pipeline/scripts/destroy_*.sh`

## References

- [RKE2 Documentation](https://docs.rke2.io/)
- [Rancher Documentation](https://rancher.com/docs/)
- [QA Infrastructure Automation Repository](https://github.com/rancher/qa-infra-automation)
- [OpenTofu Documentation](https://opentofu.org/docs/)
- [Terraform AWS Provider](https://registry.terraform.io/providers/hashicorp/aws/latest/docs)
- [Ansible Documentation](https://docs.ansible.com/)
- [AWS S3 Backend Configuration](https://developer.hashicorp.com/terraform/language/settings/backends/s3)
