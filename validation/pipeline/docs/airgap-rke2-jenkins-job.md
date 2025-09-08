# Airgap RKE2 Jenkins Job Documentation

## Overview

This Jenkins job automates the deployment of RKE2 clusters in airgap environments using Ansible playbooks from the [rancher/qa-infra-automation](https://github.com/rancher/qa-infra-automation) repository. The job provisions infrastructure with Terraform, deploys RKE2 in airgap mode, and installs Rancher on the cluster.

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

### Log Locations
- Jenkins console output contains all deployment logs
- Kubeconfig is archived as a build artifact
- Terraform state is archived for infrastructure debugging

### Manual Cleanup
If automatic cleanup fails, manually destroy resources:

```bash
# SSH to bastion host
ssh -i /path/to/key ubuntu@<bastion-ip>

# Navigate to terraform directory
cd /root/go/src/github.com/rancher/qa-infra-automation/terraform/aws/cluster_nodes

# Destroy infrastructure
terraform workspace select jenkins_airgap_workspace
terraform destroy -auto-approve -var-file=cluster.tfvars
```

## Monitoring and Alerts

### Success Indicators
- All pipeline stages complete successfully
- Cluster nodes are in Ready state
- Rancher UI is accessible
- All system pods are running

### Failure Indicators
- Terraform apply failures
- Ansible playbook errors
- Cluster validation failures
- Rancher deployment issues

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

## References

- [RKE2 Documentation](https://docs.rke2.io/)
- [Rancher Documentation](https://rancher.com/docs/)
- [QA Infrastructure Automation Repository](https://github.com/rancher/qa-infra-automation)
- [Terraform AWS Provider](https://registry.terraform.io/providers/hashicorp/aws/latest/docs)
- [Ansible Documentation](https://docs.ansible.com/)
