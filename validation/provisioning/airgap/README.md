# Airgap Provisioning Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general provisioning](../README.md)

As we are operating within an airgapped environment, you will not be able to connect in your browser without first connecting via a jump host. The easiest way to do this is with the following command: `ssh -i <PEM file> -f -N -L 8443:<Rancher FQDN:443 <username>@<Bastion public IP>`.

The above command will connect your client node to the same network that your airgapped environment, thus allowing you to access in your browser Additionally, in your client node's `/etc/hosts` file, temporarily update to have the following entry:

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults)

### ACE Test

#### Description: 
ACE(Authorized Cluster Endpoint) test verifies that a custom cluster can be provisioned with ACE enabled

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `K3S_Airgap_ACE`
2. `RKE2_Airgap_ACE`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/airgap --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestAirgapK3SACE -timeout=1h -v`
2. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/airgap --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestAirgapRKE2ACE -timeout=1h -v`

### Custom Test

#### Description: 
Custom test verfies that various custom cluster configurations provision properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `RKE2_Airgap_Custom`
2. `RKE2_Airgap_Custom_Windows`
3. `K3S_Airgap_Custom`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/airgap --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCustomRKE2Airgap -timeout=1h -v`
2. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/airgap --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCustomK3SAirgap -timeout=1h -v`

### UI Offline Preferred Setting Test

#### Description: 
UI Offline Preferred Setting test that tests accessbility to v1 and v3 endpoints with various UI offline preferred settings.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `UI_Offline_Preferred_Dynamic`
2. `UI_Offline_Preferred_Local`
3. `UI_Offline_Preferred_Remote`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/airgap --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestUIOfflinePreferred -timeout=1h -v`

## Configurations

### Cluster Config
clusterConfig is needed to the run the all RKE2 tests. If no cluster config is provided all values have defaults.

**nodeProviders is only needed for custom cluster tests; the framework only supports custom clusters through aws/ec2 instances.**
```yaml
terraform:
  airgapBastion: ""
  privateKeyPath: ""
  privateRegistries:                          # This is an optional block. You must already have a private registry stood up
    url: ""

clusterConfig:
  bastionUser: ""
  bastionWindowsUser: ""
  machinePools:
  - machinePoolConfig:
      etcd: true
      quantity: 1
  - machinePoolConfig:
      controlplane: true
      quantity: 1
  - machinePoolConfig:
      worker: true
      quantity: 1
  kubernetesVersion: ""
  cni: "calico"
  provider: "aws"
  nodeProvider: "ec2"
  hardened: false
  compliance: false                   #Set this to true for rancher versions with compliance (2.12+)
  psact: ""                           #either rancher-privileged|rancher-restricted|rancher-baseline
  
  etcd:
    disableSnapshot: false
    snapshotScheduleCron: "0 */5 * * *"
    snapshotRetain: 3
    s3:
      bucket: ""
      endpoint: "s3.us-east-2.amazonaws.com"
      endpointCA: ""
      folder: ""
      region: "us-east-2"
      skipSSLVerify: true
```


#### Custom Cluster Config
Custom clusters are only supported on AWS.
```yaml
  awsEC2Configs:
    region: "us-east-2"
    awsSecretAccessKey: ""
    awsAccessKeyID: ""
    awsEC2Config:
      - instanceType: "t3a.medium"
        awsRegionAZ: ""
        awsAMI: ""
        awsSecurityGroups: [""]
        awsSubnetID: ""
        awsSSHKeyName: ""
        awsCICDInstanceTag: "rancher-validation"
        awsIAMProfile: ""
        awsUser: "ubuntu"
        volumeSize: 50
        roles: ["etcd", "controlplane"]
      - instanceType: "t3a.medium"
        awsRegionAZ: ""
        awsAMI: ""
        awsSecurityGroups: [""]
        awsSubnetID: ""
        awsSSHKeyName: ""
        awsCICDInstanceTag: "rancher-validation"
        awsIAMProfile: ""
        awsUser: "ubuntu"
        volumeSize: 50
        roles: ["worker"]
      - instanceType: "t3a.xlarge"
        awsAMI: ""
        awsSecurityGroups: [""]
        awsSubnetID: ""
        awsSSHKeyName: ""
        awsCICDInstanceTag: "rancher-validation"
        awsUser: "Administrator"
        volumeSize: 50
        roles: ["windows"]
```

## Defaults
This package contains a defaults folder which contains default test configuration data for non-sensitive fields. The goal of this data is to: 
1. Reduce the number of fields the user needs to provide in the cattle_config file. 
2. Reduce the amount of yaml data that needs to be stored in our pipelines.
3. Make it easier to run tests

Any data the user provides will override these defaults which are stored here: [defaults](defaults/defaults.yaml). 


## Logging
This package supports several logging levels. You can set the logging levels via the cattle config and all levels above the provided level will be logged while all logs below that logging level will be omitted. 

```yaml
logging:
   level: "trace" #trace debug, info, warning, error
```

## Additional
1. If the tests passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
2. All of the tests utilize parallelism when running for more finite control of how things are run in parallel use the -p and -parallel.