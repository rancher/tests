# Longhorn interoperability tests

This directory contains tests for interoperability between Rancher and Longhorn. The list of planned tests can be seen on `schemas/pit-schemas.yaml` and the implementation for the ones that are automated is contained in `longhorn_test.go`.

## Running the tests

This package contains two test suites:
1. `TestLonghornChartTestSuite`: Tests envolving installing Longhorn through Rancher Charts.
2. `TestLonghornTestSuite`: Tests that handle various other Longhorn use cases, can be run with a custom pre-installed Longhorn.

Additional configuration for both suites can be included in the Cattle Config file as follows:

```yaml
longhorn:
  testProject: "longhorn-custom-test"
  testStorageClass: "longhorn" # Can be "longhorn", "longhorn-static" or a custom storage class if you have one.
```

If no additional configuration is provided, the default project name `longhorn-test` and the storage class `longhorn` are used.
