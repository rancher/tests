# Ext Kubeconfigs Test Suite (Public API)

This repository contains Golang automation tests for Ext Kubeconfigs (Public API).

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Setup

Your GO suite should be set to `-run ^TestKubeconfigTestSuite$`. You can find specific tests by checking the test file you plan to run.

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "downstream_cluster_name"
awsCredentials:
 accessKey: "<Your Access Key>" #edit as needed
 secretKey: "<Your Secret Key>" #edit as needed
 defaultRegion: "us-east-2" 

clusterConfig:
  machinePools:
  - machinePoolConfig:
      etcd: true
      controlplane: true
      worker: true
      quantity: 1
      drainBeforeDelete: true
      hostnameLengthLimit: 29
      nodeStartupTimeout: "600s"
      unhealthyNodeTimeout: "300s"
      maxUnhealthy: "2"
      unhealthyRange: "2-4"
  kubernetesVersion: "v1.32.7+k3s1" #edit as needed
  provider: "aws"
  cni: "calico"
  nodeProvider: "ec2"
  networking:
    stackPreference: "ipv4"
  hardened: false
 
awsMachineConfigs:
 region: "us-east-2"
 awsMachineConfig:
 - roles: ["etcd","controlplane","worker"]
   ami: "ami-012fd49f6b0c404c7"
   instanceType: "t3a.xlarge"                
   sshUser: "ubuntu"
   vpcId: "<VPC ID>" #edit as needed
   volumeType: "gp2"                         
   zone: "a"
   retries: "5"                              
   rootSize: "100"                            
   securityGroup: ["<SECURITY GROUP NAME>"] #edit as needed
sshPath:
 sshPath: "<Your ssh path>"
```
