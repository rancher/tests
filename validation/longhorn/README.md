# Longhorn interoperability tests

This directory contains tests for interoperability between Rancher and Longhorn. The list of planned tests can be seen on `schemas/pit-schemas.yaml` and the implementation for the ones that are automated is contained in `longhorn_test.go`.

## Running the tests

To run all available tests, one just has to run the `TestLonghornTestSuite`. Additional configuration can be included in the Cattle Config file as follows:

```yaml
longhorn:
  testProject: "longhorn-custom-test"
  testStorageClass: "longhorn" # Can be either "longhorn" or "longhorn-static"
```

If no additional configuration is provided, the default project name `longhorn-test` and the storage class `longhorn` are used.
