# K3K (Kubernetes in Kubernetes) Provisioning Tests

This directory contains test cases for provisioning and verifying k3k clusters in Rancher.

## Test Files

- **custom_test.go**: Tests for provisioning k3k clusters using custom node providers
- **node_driver_test.go**: Tests for provisioning k3k clusters using node drivers

## Overview

k3k (Kubernetes in Kubernetes) is a cluster provisioning type that allows running Kubernetes clusters within Kubernetes clusters. These tests verify that:

1. k3k clusters can be provisioned successfully through Rancher
2. Clusters reach a ready state after provisioning
3. Cluster deployments are correctly configured
4. Cluster pods are running properly
5. Workloads can be deployed and verified

## Test Patterns

The tests follow the same pattern as RKE2 and K3S tests:
- Multiple node pool configurations (all roles, shared roles, dedicated roles)
- Standard user client for provisioning
- Comprehensive verification steps
- Workload creation and verification

## Configuration

Test configuration is loaded from:
- `defaults/defaults.yaml` - Package-specific defaults
- Environment-specified configuration file

## References

- cluster-api-provider-k3k: https://github.com/rancher/cluster-api-provider-k3k
- Rancher API: https://github.com/rancher/rancher
