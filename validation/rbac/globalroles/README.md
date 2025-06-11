# Global Roles

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the global_roles_test.go, set the GO suite to `-run ^TestGlobalRolesTestSuite$`
- To run the rbac_global_roles_test.go, set the GO suite to `-run ^TestRbacGlobalRolesTestSuite$`

In your config file, set the following:

```yaml
rancher: 
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "cluster_name"
```
For the restrictedadmin_replacement_role_test.go run, we need the following additional parameters to be passed in the config file as we create a downstream k3s cluster:

```yaml
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
  - machinePoolConfig:
      worker: true
      quantity: 2
  - machinePoolConfig:
      windows: true
      quantity: 1
  kubernetesVersion: ""
  provider: "aws"
  cni: "calico"
  nodeProvider: "ec2"
  hardened: false

awsCredentials:
 accessKey: ""
 secretKey: ""
 defaultRegion: ""
 
awsMachineConfigs:
 region: ""
 awsMachineConfig:
 - roles: ["etcd","controlplane","worker"]
   ami: ""
   instanceType: "t3a.medium"                
   sshUser: "ubuntu"
   vpcId: ""
   volumeType: "gp2"                         
   zone: "a"
   retries: "5"                              
   rootSize: "60"                            
   securityGroup: []
```
