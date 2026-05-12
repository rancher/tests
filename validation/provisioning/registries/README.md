# Registry Provisioning Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general provisioning](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults)

### Auth Registry Test

#### Description: 
Authenticated private registries test verifies that a downstream cluster can be provisioned with an authenticated private registry

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `Auth_RKE2`
2. `Auth_K3S`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/registries --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestAuthenticatedRegistry -timeout=1h -v`

### ECR Test

#### Description: 
ECR test verifies that a downstream cluster can be provisioned with an ECR private registry

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `ECR_RKE2`
2. `ECR_K3S`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/registries --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestECR -timeout=1h -v`

### Global Registry Test

#### Description: 
Global private registries test verifies that a downstream cluster can be provisioned with a global private registry

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `Global_RKE2`
2. `Global_RKE2_Windows`
3. `Global_K3S`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/registries --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestGlobalRegistry -timeout=1h -v`
2. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/registries --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestGlobalRegistryWindows -timeout=1h -v`

### Non-Auth Registry Test

#### Description: 
Non-authenticated private registries test verifies that a downstream cluster can be provisioned with a non-authenticated private registry

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `Non_Auth_RKE2`
2. `Non_Auth_K3S`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/registries --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestNonAuthenticatedRegistry -timeout=1h -v`


## Configurations

### Cluster Config
clusterConfig is needed to the run the all RKE2 tests. If no cluster config is provided all values have defaults.

**nodeProviders is only needed for custom cluster tests; the framework only supports custom clusters through aws/ec2 instances.**
```yaml
terraform:
  privateKeyPath: ""
  privateRegistries:                          # This is an optional block. You must already have a private registry stood up
    url: ""

clusterConfig:
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

### Cloud Credential Config
Cloud credentials for various cloud providers.

#### AWS
```yaml
awsCredentials:                       #required (all) for AWS
  secretKey: ""
  accessKey: ""
  defaultRegion: ""
```

### Machine Config
Machine config is needed for tests that provision node driver clusters. 

#### AWS Machine Config
```yaml
awsMachineConfigs:                            #default              
  region: "us-east-2"
  awsMachineConfig:
  - roles: ["etcd","controlplane","worker"]
    ami: ""                                   #required
    enablePrimaryIPv6: true
    httpProtocolIpv6: "enabled"
    ipv6AddressOnly: true
    ipv6AddressCount: "1"
    instanceType: "t3a.medium"
    sshUser: "ubuntu"                         #required
    vpcId: ""                                 #required
    volumeType: "gp3"                         
    zone: "a"
    retries: "5"                              
    rootSize: "100"                            
    securityGroup: [""]                       #required                       
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