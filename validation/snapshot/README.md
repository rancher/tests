# Snapshot

The snapshot package tests taking etcd snapshots on a downstream cluster in Rancher and then restoring that snapshot in Rancher. The following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Create workloads prior to taking an etcd snapshot
4. Take an etcd snapshot on the downstream cluster
5. Depending on the test, upgrade the K8s version of the downstream cluster
6. Create workloads post taking an etcd snapshot
7. Restore the etcd snapshot on the downstream cluster
8. Validate if the workloads created post taking an etcd snapshot no longer exist

Please see below for more details for your config. Please note that the config can be in either JSON or YAML (all examples are illustrated in YAML).

## Table of Contents
1. [Getting Started](#Getting-Started)
2. [Running Tests](#Running-Tests)

## Cluster Configuration
If the user doesn't provide an existing cluster via the rancher.clusterName you can find configuration details for node driver/custom clusters here: [provisioning](../provisioning/README.md) 