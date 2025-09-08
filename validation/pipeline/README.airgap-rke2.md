# Airgap RKE2 Jenkins Job

This directory contains a complete Jenkins job implementation for deploying RKE2 clusters in airgap environments using Ansible playbooks from the [rancher/qa-infra-automation](https://github.com/rancher/qa-infra-automation) repository.

## 🚀 Quick Start

1. **Run the setup script:**
   ```bash
   ./validation/pipeline/scripts/setup_airgap_rke2_job.sh
   ```

2. **Configure Jenkins credentials** (see [documentation](docs/airgap-rke2-jenkins-job.md#jenkins-credentials))

3. **Create the Jenkins job** using the provided Jenkinsfile

4. **Test the deployment** with your configuration

## 📁 Files Created

### Core Pipeline
- [`Jenkinsfile.airgap.rke2`](Jenkinsfile.airgap.rke2) - Main Jenkins pipeline script
- [`scripts/setup_airgap_rke2_job.sh`](scripts/setup_airgap_rke2_job.sh) - Setup and validation script

### Configuration Templates
- [`configs/airgap-rke2-ansible-vars.yaml.template`](configs/airgap-rke2-ansible-vars.yaml.template) - Ansible variables template
- [`configs/airgap-rke2-terraform.tfvars.template`](configs/airgap-rke2-terraform.tfvars.template) - Terraform variables template

### Documentation
- [`docs/airgap-rke2-jenkins-job.md`](docs/airgap-rke2-jenkins-job.md) - Complete documentation and troubleshooting guide

## 🏗️ Architecture

The Jenkins job deploys a complete airgap RKE2 environment:

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Bastion Host  │    │ Private Registry│    │  Load Balancer  │
│   (Internet)    │    │   (Airgap)      │    │   (Internal)    │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
         ┌───────────────────────┼───────────────────────┐
         │                       │                       │
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│ RKE2 Server #1  │    │ RKE2 Server #2  │    │ RKE2 Server #3  │
│ (Control Plane) │    │ (Control Plane) │    │ (Control Plane) │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
         ┌───────────────────────┼───────────────────────┐
         │                       │                       │
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│ RKE2 Agent #1   │    │ RKE2 Agent #2   │    │ RKE2 Agent #N   │
│   (Worker)      │    │   (Worker)      │    │   (Worker)      │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## 🔧 Pipeline Stages

1. **Checkout Repositories** - Clone rancher/tests and rancher/qa-infra-automation
2. **Configure Environment** - Process templates and setup credentials
3. **Setup Airgap Infrastructure** - Provision AWS resources with Terraform
4. **Deploy RKE2 Airgap Cluster** - Install RKE2 in airgap mode
5. **Deploy Rancher on Airgap Cluster** - Install Rancher with Helm
6. **Validate Airgap Deployment** - Verify cluster health and functionality
7. **Cleanup Resources** - Destroy infrastructure (optional)
8. **Archive Results** - Save kubeconfig and Terraform state

## 🔐 Security Features

- **Secure Credential Management** - All sensitive data stored in Jenkins credentials
- **SSH Key Management** - Automated SSH key setup and permissions
- **Network Isolation** - Agent nodes have no internet access
- **Registry Security** - Private registry with authentication
- **Immutable Deployments** - Infrastructure as code with Terraform

## 🛠️ Key Features

- **Complete Airgap Support** - No internet access required for cluster nodes
- **High Availability** - Multi-master RKE2 deployment
- **Automated Rollback** - Infrastructure cleanup on deployment failures
- **Comprehensive Logging** - Detailed logs for troubleshooting
- **Artifact Archival** - Kubeconfig and state files preserved
- **Configurable Resources** - Flexible instance types and counts
- **Security Hardening** - Best practices for airgap environments

## 📋 Prerequisites

### Jenkins Credentials Required
- AWS access keys and SSH keys
- Private registry credentials
- System passwords and tokens
- Optional Slack webhook for notifications

### Environment Variables Required
- AWS region, VPC, and networking configuration
- RKE2 and Rancher versions
- Timeout and cleanup settings

See the [complete documentation](docs/airgap-rke2-jenkins-job.md) for detailed requirements.

## 🚦 Usage

### Basic Deployment
```bash
# Set required environment variables
export AWS_REGION="us-west-2"
export AWS_VPC="vpc-12345678"
export RKE2_VERSION="v1.28.8+rke2r1"
export RANCHER_VERSION="2.8.3"

# Run the Jenkins job with your configurations
```

### Custom Configuration
1. Copy and customize the configuration templates
2. Update Jenkins job parameters
3. Run the deployment

### Cleanup
Set `CLEANUP_RESOURCES=true` to automatically destroy infrastructure after testing.

## 🔍 Monitoring and Troubleshooting

- **Jenkins Console Logs** - Primary source for deployment status
- **Archived Artifacts** - Kubeconfig and Terraform state files
- **Slack Notifications** - Optional deployment status updates
- **Manual Cleanup** - Instructions for failed deployments

## 📚 Integration with rancher/qa-infra-automation

This Jenkins job leverages the airgap RKE2 playbooks from the rancher/qa-infra-automation repository:

- **Terraform Modules** - AWS infrastructure provisioning
- **Ansible Playbooks** - RKE2 and Rancher deployment
- **Configuration Management** - Airgap-specific settings
- **Image Management** - Private registry setup and population

## 🤝 Contributing

To extend or modify this Jenkins job:

1. Update the Jenkinsfile for pipeline changes
2. Modify configuration templates for new parameters
3. Update documentation for new features
4. Test thoroughly in a development environment

## 📞 Support

For issues and questions:
- Check the [troubleshooting guide](docs/airgap-rke2-jenkins-job.md#troubleshooting)
- Review Jenkins console logs
- Consult the rancher/qa-infra-automation repository
- Contact the QA infrastructure team

## 🔗 Related Resources

- [RKE2 Documentation](https://docs.rke2.io/)
- [Rancher Documentation](https://rancher.com/docs/)
- [QA Infrastructure Automation](https://github.com/rancher/qa-infra-automation)
- [Jenkins Pipeline Documentation](https://www.jenkins.io/doc/book/pipeline/)

---

**Created by:** DevOps Automation Specialist  
**Last Updated:** $(date +"%Y-%m-%d")  
**Version:** 1.0.0
