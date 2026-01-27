# K3K Provisioning Tests

This directory contains tests for provisioning and verifying K3K (Kubernetes on K3s) clusters in Rancher using the cluster-api-provider-k3k.

## Overview

K3K is a Cluster API provider for K3s, enabling the provisioning of K3s-based Kubernetes clusters through the Cluster API. These tests verify that K3K clusters can be successfully provisioned and managed through Rancher.

## Test Files

- `custom_test.go` - Tests for provisioning K3K clusters using custom node providers (e.g., EC2 instances)
- `node_driver_test.go` - Tests for provisioning K3K clusters using Rancher node drivers
- `dynamic_custom_test.go` - Dynamic tests for custom cluster provisioning with user-specified configurations
- `dynamic_node_driver_test.go` - Dynamic tests for node driver cluster provisioning with user-specified configurations

## Build Tags

Tests use the following build tags:
- `validation` - Standard validation tests
- `recurring` - Tests that run on a recurring schedule
- `pit.daily` - Product Integration Tests that run daily

## Test Configurations

### Custom Cluster Tests
These tests provision K3K clusters by registering custom nodes (e.g., EC2 instances) with various node role configurations:
- All roles on single node pool
- Shared roles (etcd+controlplane, worker)
- Dedicated roles (etcd, controlplane, worker)
- Standard HA configuration (3 etcd, 2 controlplane, 3 worker)

### Node Driver Tests
These tests provision K3K clusters using Rancher's node driver providers (AWS, Azure, vSphere, etc.) with similar node role configurations.

## Configuration

Tests require a `cattle-config.yaml` file with the following configurations:
- Rancher connection details (host, token)
- Cloud provider credentials
- Machine configurations
- Network settings

See `validation/provisioning/defaults/defaults.yaml` for the base configuration template.

## Running Tests

```bash
# Run all K3K validation tests
go test -tags validation -timeout=60m ./validation/provisioning/k3k/...

# Run specific test
go test -tags validation -timeout=60m -run TestCustom ./validation/provisioning/k3k/

# Run with custom config
CATTLE_TEST_CONFIG=/path/to/config.yaml go test -tags validation -timeout=60m ./validation/provisioning/k3k/...
```

## References

- [cluster-api-provider-k3k](https://github.com/rancher/cluster-api-provider-k3k) - K3K provider implementation
- [Rancher API](https://github.com/rancher/rancher) - Rancher API documentation
