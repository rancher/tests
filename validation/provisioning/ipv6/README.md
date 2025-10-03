# IPv6 Provisioning Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general provisioning](../README.md)


## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults)

### Custom Test

#### Description: 
Custom test verfies that various custom cluster configurations provision properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `RKE2_IPv6_Custom_CIDR`
2. `RKE2_IPv6_Custom_Stack_Preference`
3. `RKE2_IPv6_Custom_CIDR_Stack_Preference`
4. `K3S_IPv6_Custom_CIDR`
5. `K3S_IPv6_Custom_Stack_Preference`
6. `K3S_IPv6_Custom_CIDR_Stack_Preference`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCustomRKE2IPv6 -timeout=1h -v`
2. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCustomK3SIPv6 -timeout=1h -v`

### Node Driver Test

#### Description: 
Node driver test verfies that various node driver cluster configurations provision properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests
1. `RKE2_IPv6_Node_Driver_CIDR`
2. `RKE2_IPv6_Node_Driver_CIDR_Stack_Preference`
3. `K3S_IPv6_Node_Driver_CIDR`
4. `K3S_IPv6_Node_Driver_CIDR_Stack_Preference`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestNodeDriverRKE2IPv6 -timeout=1h -v`
2. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestNodeDriverK3SIPv6 -timeout=1h -v`

## Configurations

### Cluster Config
clusterConfig is needed to the run the all RKE2 tests. If no cluster config is provided all values have defaults.

**nodeProviders is only needed for custom cluster tests; the framework only supports custom clusters through aws/ec2 instances.**
```yaml
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

#### Digital Ocean
```yaml
digitalOceanCredentials:             #required (all) for DO
  accessToken": ""
```

#### Linode
```yaml
linodeCredentials:                    #required (all) for Linode
  token: ""
```

#### Azure
```yaml
azureCredentials:                     #required (all) for Azure
  clientId: ""
  clientSecret: ""
  subscriptionId": ""
  environment: "AzurePublicCloud"
```

#### Harvester
```yaml
harvesterCredentials:                 #required (all) for Harvester
  clusterId: ""
  clusterType: ""
  kubeconfigContent: ""
```

#### Google
```yaml
googleCredentials:                    #required (all) for Google
  authEncodedJson: ""
```

#### VSphere
```yaml
vmwarevsphereCredentials:             #required (all) for VMware
  password: ""
  username: ""
  vcenter: ""
  vcenterPort: ""
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

#### Digital Ocean Machine Config
```yaml
doMachineConfigs:                              #required (all)
  region: "nyc3"
  doMachineConfig:
  - roles: ["etcd","controlplane","worker"]
    image: "ubuntu-20-04-x64"
    backups: false
    ipv6: true
    monitoring: false
    privateNetworking: false
    size: "s-2vcpu-4gb"
    sshKeyContents: ""
    sshKeyFingerprint: ""
    sshPort: "22"
    sshUser: "root"
    tags: ""
    userdata: ""
```

#### Linode Machine Config
```yaml
linodeMachineConfigs:                           #required (all)
  region: "us-west"
  linodeMachineConfig:
  - roles: ["etcd","controlplane","worker"]
    authorizedUsers: ""
    createPrivateIp: true
    dockerPort: "2376"
    image: "linode/ubuntu22.04"
    instanceType: "g6-standard-8"
    rootPass: ""
    sshPort: "22"
    sshUser: ""
    stackscript: ""
    stackscriptData: ""
    swapSize: "512"
    tags: ""
    uaPrefix: "Rancher"
```

#### Azure Machine Config
```yaml
azureMachineConfigs:                            #required (all)
  environment: "AzurePublicCloud"
  azureMachineConfig:
  - roles: ["etcd","controlplane","worker"]
    availabilitySet: "docker-machine"
    diskSize: "30"
    faultDomainCount: "3"
    image: "canonical:UbuntuServer:22.04-LTS:latest"
    location: "westus"
    managedDisks: false
    noPublicIp: false
    nsg: ""
    openPort: ["6443/tcp", "2379/tcp", "2380/tcp", "8472/udp", "4789/udp", "9796/tcp", "10256/tcp", "10250/tcp", "10251/tcp", "10252/tcp"]
    resourceGroup: "docker-machine"
    size: "Standard_D2_v2"
    sshUser: "docker-user"
    staticPublicIp: false
    storageType: "Standard_LRS"
    subnet: "docker-machine"
    subnetPrefix: "x.x.x.x/xx"
    updateDomainCount: "5"
    usePrivateIp: false
    vnet: "docker-machine-vnet"
```

#### Harvester Machine Config
```yaml
harvesterMachineConfigs:                        #required (all)
  vmNamespace: "default"
  harvesterMachineConfig:
  - roles: ["etcd","controlplane","worker"]
    diskSize: "40"
    cpuCount: "2"
    memorySize: "8"
    networkName: "default/ctw-network-1"
    imageName: "default/image-rpj98"
    sshUser: "ubuntu"
    diskBus: "virtio
```

#### Vsphere Machine Config
```yaml
vmwarevsphereMachineConfigs:                    #required (all)
    datacenter: "/<datacenter>"
    hostSystem: "/<datacenter>/path-to-host"
    datastore: "/<datacenter>/path-to-datastore" 
    datastoreURL: "ds:///<url>"             
    folder: "/<datacenter>/path-to-vm-folder" 
    pool: "/<datacenter>/path-to-resource-pool" 
    vmwarevsphereMachineConfig:
    - cfgparam: ["disk.enableUUID=TRUE"]
      cloudConfig: "#cloud-config\n\n"
      customAttribute: []
      tag: []
      roles: ["etcd","controlplane",worker]
      creationType: "template"
      os: "linux"
      cloneFrom: "/<datacenter>/path-to-linux-image"
      cloneFromWindows: "/<datacenter>/path-to-windows-image"
      contentLibrary: ""                                        
      datastoreCluster: ""
      network: ["/<datacenter>/path-to-vm-network"]
      sshUser: ""
      sshPassword: ""                                           
      sshUserGroup: ""
      sshPort: "22"
      cpuCount: "4"
      diskSize: "40000"
      memorySize: "8192"
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