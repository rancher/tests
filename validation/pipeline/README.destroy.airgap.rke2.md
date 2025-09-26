# Destroy Airgap RKE2 Pipeline

This pipeline is responsible for tearing down the airgap RKE2 infrastructure and cleaning up resources.

## Required Parameters

The following parameters are required for the pipeline to function correctly:

### S3 Parameters
- `S3_BUCKET_NAME`: The S3 bucket name for Terraform state storage
  - Default: `jenkins-terraform-state-storage`
  - Must be a valid S3 bucket name that exists in your AWS account

- `S3_KEY_PREFIX`: The key prefix for Terraform state files
  - Default: `jenkins-airgap-rke2/terraform.tfstate`
  - Must match the prefix used during infrastructure creation

### AWS Parameters
- `AWS_ACCESS_KEY_ID`: AWS access key ID
  - Stored as Jenkins credential: `aws-access-key-id`
  - Must have read/write access to the S3 bucket

- `AWS_SECRET_ACCESS_KEY`: AWS secret access key
  - Stored as Jenkins credential: `aws-secret-access-key`
  - Must have read/write access to the S3 bucket

- `AWS_REGION`: AWS region
  - Default: `us-east-2`
  - Must be the same region where the infrastructure was created

## Pipeline Stages

1. **Checkout**: Checks out the repository code
2. **Download Config**: Downloads the Terraform configuration from S3
3. **Destroy Infrastructure**: Destroys the infrastructure using Terraform

## Prerequisites

Before running this pipeline:

1. Ensure the infrastructure was created using the corresponding `Jenkinsfile.airgap.rke2` pipeline
2. Verify that the S3 bucket and key prefix match those used during creation
3. Confirm that AWS credentials have the necessary permissions

## Troubleshooting

### Common Issues

1. **S3 access denied**
   - Verify AWS credentials have proper permissions
   - Check that the S3 bucket exists and is accessible

2. **State file not found**
   - Ensure the S3_KEY_PREFIX matches exactly what was used during creation
   - Check that the state file exists in the specified S3 location

3. **Infrastructure destroy fails**
   - Check that all resources are in a state that allows destruction
   - Verify there are no dependent resources that need to be removed first

### Debug Steps

1. Check the pipeline logs for specific error messages
2. Verify the downloaded configuration matches what was expected
3. Manually run Terraform commands to debug issues

## Cleanup

The pipeline includes automatic cleanup of:
- Workspace files
- Archived artifacts (tfplan, terraform.tfstate, terraform.tfstate.backup)

## Notifications

On failure, the pipeline sends a Slack notification if running on the main branch.