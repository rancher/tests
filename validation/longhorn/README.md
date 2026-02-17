# Longhorn interoperability tests

This directory contains tests for interoperability between Rancher and Longhorn. The list of planned tests can be seen on `schemas/pit-schemas.yaml` and the implementation for the ones that are automated is contained in `longhorn_test.go`.

## Running the tests

This package contains three test suites:

1. `TestLonghornChartTestSuite`: Tests involving installing Longhorn through Rancher Charts.
2. `TestLonghornTestSuite`: Tests that handle various other Longhorn use cases, can be run with a custom pre-installed Longhorn.
3. `TestLonghornUIAccessTestSuite`: Tests that validate Longhorn UI/API access and functionality on downstream clusters.

Additional configuration for all suites can be included in the Cattle Config file as follows:

```yaml
longhorn:
  testProject: "longhorn-custom-test"
  testStorageClass: "longhorn" # Can be "longhorn", "longhorn-static" or a custom storage class if you have one.
```

If no additional configuration is provided, the default project name `longhorn-test` and the storage class `longhorn` are used.

## Longhorn UI Access Test

The `TestLonghornUIAccessTestSuite` validates Longhorn UI and API access on a downstream Rancher cluster. It performs the following checks:

1. **Pod Validation**: Verifies all pods in the `longhorn-system` namespace are in an active/running state
2. **Service Accessibility**: Checks that the Longhorn frontend service is accessible and returns valid HTTP responses
   - Supports ClusterIP (via proxy), NodePort, and LoadBalancer service types
3. **Longhorn API Validation**:
   - Validates Longhorn nodes are in a valid state
   - Validates Longhorn settings are properly configured
   - Creates a test volume via the Longhorn API
   - Verifies the volume is active through both Longhorn and Rancher APIs
   - Validates the volume uses the correct Longhorn storage class

### Test Methods

- `TestLonghornUIAccess`: Static test that validates core functionality without user-provided configuration
- `TestLonghornUIDynamic`: Dynamic test that validates configuration based on user-provided settings in the config file

### Running the UI Access Test

```bash
go test -v -tags "validation" -run TestLonghornUIAccessTestSuite ./validation/longhorn/
```

Or with specific test methods:

```bash
go test -v -tags "validation" -run TestLonghornUIAccessTestSuite/TestLonghornUIAccess ./validation/longhorn/
go test -v -tags "validation" -run TestLonghornUIAccessTestSuite/TestLonghornUIDynamic ./validation/longhorn/
```

### Prerequisites

- Longhorn must be installed on the downstream cluster (either pre-installed or installed by the test suite)
- The cluster must be accessible via Rancher
- The test requires network access to the Longhorn service in the downstream cluster
