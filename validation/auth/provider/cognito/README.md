## AWS Cognito Authentication Tests

This package contains tests for AWS Cognito authentication provider functionality in Rancher.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [AWS Cognito Test Configuration](#aws-cognito-test-configuration)
  - [What Setup Creates](#what-setup-creates)
  - [Running the Tests](#running-the-tests)

## Prerequisites

- An AWS account and an access key that may administer Cognito user pools
- Rancher reachable at the address in `rancher.host`, because that is the address Cognito redirects back to

Nothing else has to exist beforehand.

## Configuration

### Rancher Configuration

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  clusterName: "cluster_to_run_tests_on"
  insecure: true
  cleanup: false
```

### AWS Cognito Test Configuration

```yaml
cognito:
  region: "us-east-2"
  accessKey: "<aws-access-key-id>"
  secretKey: "<aws-secret-access-key>"
```

### What Setup Creates

The suite builds its own identity provider, so the account needs nothing in it beforehand. Setup creates the following, and all of it is deleted when the suite finishes:

- **The user pool**, and **a hosted UI domain** sharing its name.
- **The Rancher app client**, confidential, with `https://<rancher.host>/verify-auth` as its callback.
- **The administrator account** whose OpenID Connect login enables the provider.
- **A group with two members**, plus one account outside it.

### Running the Tests
**Run AWS Cognito Authentication Tests**
Your GO suite should be set to `-run ^TestCognitoAuthProviderSuite$`

**Example:**
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/cognito --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestCognitoAuthProviderSuite$`
