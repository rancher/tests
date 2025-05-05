# Certificate Tests

This package contains tests for certificate management and rotation. The tests are organized into two main test suites.

## Table of Contents
- [Certificate Tests](#certificate-tests)
  - [Table of Contents](#table-of-contents)
  - [Certificate Functional Tests](#certificate-functional-tests)
  - [Certificate Rotation Tests](#certificate-rotation-tests)
  - [Getting Started](#getting-started)
  - [Running the Tests](#running-the-tests)
    - [Run Certificate Functional Tests](#run-certificate-functional-tests)
    - [Run Certificate Rotation Tests](#run-certificate-rotation-tests)

## Certificate Functional Tests
The certificate functional tests validate core certificate functionality in Kubernetes clusters managed by Rancher. These tests ensure that certificates can be properly created, managed, and used with ingress resources across different namespaces and projects.

Note: RBAC tests for certificates are covered in `rbac/certificates`

## Certificate Rotation Tests
These tests are designed to accept an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, you should create one first before running these tests.

Please see below for more details for your config. Please note that the config can be in either JSON or YAML (all examples are illustrated in YAML).

## Getting Started
In your config file, set the following:
```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  clusterName: "cluster_to_run_tests_on"
  insecure: true/optional
  cleanup: false/optional
```

Typically, a cluster with the following 3 pools is used for testing:
```yaml
{
  {
    ControlPlane: true,
    Quantity:     1,
  },
  {
    Etcd:     true,
    Quantity: 1,
  },
  {
    Worker:   true,
    Quantity: 1,
  },
}
```

## Running the Tests
These tests utilize Go build tags. Due to this, use the commands below to run the tests:

### Run Certificate Functional Tests
Your GO suite should be set to `-run ^TestCertificateTestSuite$`

Example:
```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestCertificateTestSuite$
```

### Run Certificate Rotation Tests
Your GO suite should be set to `-run ^TestCertRotationTestSuite$`

Example:
```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certrotation --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestCertRotationTestSuite$
```

If the specified test passes immediately without warning, try adding the `-count=1` flag to get around this issue. This will avoid previous results from interfering with the new test run.
