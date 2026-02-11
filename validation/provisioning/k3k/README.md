# K3k Provisioning Tests

## Overview

This directory contains tests for provisioning K3k (Kubernetes in Kubernetes) clusters through Rancher.

## What is K3k?

K3k is a tool that enables creation and management of isolated K3s clusters within an existing Kubernetes environment. It provides:
- Resource isolation between embedded clusters
- Simplified multi-tenancy
- Lightweight and fast cluster provisioning
- Both shared and virtual modes for resource management
- Integration with Rancher for cluster management

## Current Status

**⚠️ IMPORTANT: K3k API endpoints are not yet available in Rancher**

As of this implementation:
- The [cluster-api-provider-k3k](https://github.com/rancher/cluster-api-provider-k3k) project is in **unreleased alpha state**
- Rancher's provisioning APIs (`provisioning.cattle.io/v1`) do not include k3k cluster types
- Shepherd extensions do not include k3k support

## Test Structure

The test files in this directory are placeholder implementations that document the expected structure and can be activated once the necessary APIs become available.

## Requirements for Implementation

When k3k APIs become available, the following will be needed:

1. **Shepherd Extensions**: Add k3k cluster creation/management functions to `github.com/rancher/shepherd/extensions/clusters`
2. **API Support**: Rancher must expose k3k cluster provisioning through Steve or Norman APIs
3. **Cluster Config**: Define k3k-specific configuration options in the provisioning input

## Test Coverage (Planned)

Once APIs are available, tests should cover:
- Basic k3k cluster provisioning
- Shared mode cluster creation
- Virtual mode cluster creation  
- Cluster verification and validation
- Resource isolation testing
- Integration with Rancher management

## References

- [K3k GitHub Repository](https://github.com/rancher/k3k)
- [cluster-api-provider-k3k](https://github.com/rancher/cluster-api-provider-k3k)
- [Rancher Provisioning API](https://github.com/rancher/rancher/tree/main/pkg/apis/provisioning.cattle.io)
