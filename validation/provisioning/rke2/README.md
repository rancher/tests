# RKE2 Provisioning Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general provisioning](../README.md)


## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults)

### ACE Test

#### Description: 
ACE(Authorized Cluster Endpoint) test verifies that a node driver cluster can be provisioned with ACE enabled

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_ACE`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestACE -timeout=1h -v`


### Agent Customization Test

#### Description: 
Agent customization test verifies that provisioning with fleet/cluster agent configured works as intended

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `Custom_Fleet_Agent`
2. `Custom_Cluster_Agent`
3. `Invalid_Custom_Fleet_Agent`
4. `Invalid_Custom_Cluster_Agent`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestAgentCustomization -timeout=1h -v`
2. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestAgentCustomizationFailure -timeout=1h -v`


### Cloud Provider Test

#### Description: 
Cloud Provider test verifies that node driver clusers can be provisioned with AWS/vSphere cloud provider

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `AWS_OutOfTree`
2. `vSphere_OutOfTree`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCloudProvider -timeout=1h -v`


### CNI Test

#### Description: 
CNI test verifies that clusters can provision properly with various CNIs.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests
1. `RKE2_Node_Driver|Calico`
2. `RKE2_Node_Driver|Canal`
3. `RKE2_Node_Driver|Flannel`
4. `RKE2_Node_Driver|Cilium`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCNI -timeout=1h -v`


### Custom Test

#### Description: 
Custom test verifies that various custom cluster configurations provision properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `RKE2_Custom|etcd_cp_worker`
2. `RKE2_Custom|etcd_cp|worker`
3. `RKE2_Custom|etcd|cp|worker`
4. `RKE2_Custom|etcd|cp|worker|windows`
5. `RKE2_Custom|3_etcd|2_cp|3_worker`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCustom -timeout=1h -v`

### Data Directories Test

#### Description: 
Data Directories test verifies that files related to k8s, systemAgent and provisioning respect the data directories feature.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `RKE2_Split_Data_Directories`
2. `RKE2_Grouped_Data_Directories`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDataDirectories -timeout=1h -v`


### Dynamic Custom Test

#### Description: 
Dynamic custom test verifies that a user defined custom cluster provisions properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
Dynamic tests do not have a static name

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDynamicCustom -timeout=1h -v`


### Dynamic Node Driver Test

#### Description: 
Dynamic node driver test verifies that a user defined node driver cluster provisions properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests
Dynamic tests do not have a static name

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDynamicNodeDriver -timeout=1h -v`


### Hardened Test

#### Description: 
Hardened test verifies that a cluster can deploy the cis-benchmark(2.11<=)/compliance(2.12+) chart on a custom cluster

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `RKE2_CIS_1.9_Profile|3_etcd|2_cp|3_worker`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestHardened -timeout=1h -v`


### Node Driver Test

#### Description: 
Node driver test verifies that various node driver cluster configurations provision properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests
1. `RKE2_Node_Driver|etcd_cp_worker`
2. `RKE2_Node_Driver|etcd_cp|worker`
3. `RKE2_Node_Driver|etcd|cp|worker`
4. `RKE2_Node_Driver|etcd|cp|worker|windows`
5. `RKE2_Node_Driver|3_etcd|2_cp|3_worker`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestNodeDriver -timeout=1h -v`


### PSACT Test

#### Description: 
PSACT(Pod Security Admission Configuration Template) Test verifies that various node driver clusters with different psact configurations provision properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests
1. `RKE2_Rancher_Privileged|3_etcd|2_cp|3_worker`
2. `RKE2_Rancher_Restricted|3_etcd|2_cp|3_worker`
3. `RKE2_Rancher_Baseline|3_etcd|2_cp|3_worker`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestPSACT -timeout=1h -v`


### Template Test

#### Description: 
Template Test verifies that an RKE2 template can be used to provision a cluster.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Template Test](#template-config)

#### Table Tests
1. `RKE2_Template|etcd|cp|worker`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestTemplate -timeout=1h -v`


### Hostname Truncation Test

#### Description: 
Hostname truncation test verifies that the node hostname is truncated properly.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests
1. `RKE2_Hostname_Truncation|10_Characters`
2. `RKE2_Hostname_Truncation|31_Characters`
3. `RKE2_Hostname_Truncation|63_Characters`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestHostnameTruncation -timeout=1h -v`

### All Tests

#### Description: 
Template Test verifies that an RKE2 template can be used to provision a cluster.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)
4. [Custom Cluster Config](#custom-cluster)
5. [Template](#template-config)

#### Table Tests
All table tests listed above except the dynamic tests

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/rke2 --junitfile results.xml --jsonfile results.json -- -tags=recurring -timeout=3h -v`



## Configurations

### Cluster Config
clusterConfig is needed to the run the all RKE2 tests. If no cluster config is provided all values have defaults.

**nodeProviders is only needed for custom cluster tests; the framework only supports custom clusters through aws/ec2 instances.**
```yaml
clusterConfig:
  machinePools:
  - machinePoolConfig:
      etcd: true
      controlplane: false
      worker: false
      quantity: 1
      drainBeforeDelete: true
      hostnameLengthLimit: 29
      nodeStartupTimeout: "600s"
      unhealthyNodeTimeout: "300s"
      maxUnhealthy: "2"
      unhealthyRange: "2-4"
  - machinePoolConfig:
      etcd: false
      controlplane: true
      worker: false
      quantity: 1
  - machinePoolConfig:
      etcd: false
      controlplane: false
      worker: true
      quantity: 1
  kubernetesVersion: ""               #Permutable in dynamic tests. Leave blank for the latest kubernetes version
  cni: "calico"                       #Permutable in dynamic tests.
  provider: "aws"                     #Permutable in dynamic tests.
  nodeProvider: "ec2"
  resourcePrefix: ""                  # OPTIONAL
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
  
  clusterAgent:                        # change this to fleetAgent for fleet agent
    appendTolerations:
    - key: "Testkey"
      value: "testValue"
      effect: "NoSchedule"
    overrideResourceRequirements:
      limits:
        cpu: "750m"
        memory: "500Mi"
      requests:
        cpu: "250m"
        memory: "250Mi"
      overrideAffinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - preference:
                matchExpressions:
                  - key: "cattle.io/cluster-agent"
                    operator: "In"
                    values:
                      - "true"
              weight: 1
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
    ipv6: false
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

### Template Config
```yaml
templateTest:
  repo:
    metadata:
      name: "templateTest"
    spec:
      gitRepo: "https://github.com/repo.git"
      gitBranch: main
      insecureSkipTLSVerify: true
  templateProvider: "aws"
  templateName: "myTemplateName"                
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