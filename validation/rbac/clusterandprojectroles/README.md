# Rbac

## Getting Started
Your GO suite should be set to `-run ^Test<>TestSuite$`. For example to run the cluster_role_test.go, set the GO suite to `-run ^TestClusterRoleTestSuite$` You can find specific tests by checking the test file you plan to run.
Config needed for each of the suites cluster_role_test.go and project_role_test.go require the following config:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "downstream_cluster_name"

awsCredentials:
 accessKey: "<access-key>>"
 secretKey: "<secret-key>"
 defaultRegion: "<region>>"

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
  kubernetesVersion: "<k8s-version>"
  provider: "aws"
  cni: "calico"
  nodeProvider: "ec2"
  networking:
    stackPreference: "ipv4"
  hardened: false
 
awsMachineConfigs:
 region: "<region>"
 awsMachineConfig:
 - roles: ["etcd","controlplane","worker"]
   ami: "<ami>"
   instanceType: "t3a.medium"                
   sshUser: "ubuntu"
   vpcId: "<vpc-id>"
   volumeType: "gp2"                         
   zone: "a"
   retries: "5"                              
   rootSize: "80"                            
   securityGroup: ["<securityGroup>"]
```

For more info, please use the following links to continue adding to your config for provisioning tests:
 [Define your test](../provisioning/rke1/README.md#provisioning-input)
(#Provisioning-Input)


