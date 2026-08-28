# Prime Configs

## Table of Contents
1. [Getting Started](#Getting-Started)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general deleting](../README.md)

## Getting Started
The config is split up into multiple parts. Think of the parts as follows:
- Standalone config for setting up Rancher
- Node driver config for provisioning downstream clusters
- Rancher config

In no particular order, see an example below:

```yaml
rancher:
  host: ""                                        # REQUIRED - fill out with the expected Rancher server URL
  adminPassword: ""                               # REQUIRED - this is the same as the bootstrapPassword below, make sure they match
  insecure: true                                  # REQUIRED - leave this as true
prime:
  brand: "suse"
  isPrime: true
  registry: "registry.rancher.com"
  sccRegistrationCode: ""
  sccRegistrationType: ""                         # REQUIRED - must be base64 encoded
terraform:
  cni: ""                                         # REQUIRED - fill with desired value
  localCluster: ""                                # REQUIRED - values are either rke2 or k3s
  provider: "aws"
  privateKeyPath: ""                              # REQUIRED - specify private key that will be used to access created instances
  privateFullChainPath: ""
  privateKeyPath: ""
  resourcePrefix: ""                              # REQUIRED - fill with desired value
  awsCredentials:
    awsAccessKey: ""
    awsSecretKey: ""
  awsConfig:
    ami: ""
    awsKeyName: ""
    awsInstanceType: ""
    awsSubnetID: ""
    awsVpcID: ""
    awsZoneLetter: ""
    awsRootSize: 100
    awsRoute53Zone: ""
    awsSecurityGroups: [""]
    awsSecurityGroupNames: [""]
    region: ""
    awsUser: ""
    sshConnectionType: "ssh"
    timeout: "5m"
    ipAddressType: "ipv4"
    loadBalancerType: "ipv4"
    targetType: "instance"
  standalone:
    bootstrapPassword: ""                         # REQUIRED - this is the same as the adminPassword above, make sure they match
    certManagerVersion: ""                        # REQUIRED - (e.g. v1.15.3)
    chartVersion: ""                              # REQUIRED - fill with desired value (leave out the leading 'v')
    rancherAgentImage: ""                         # OPTIONAL - fill out only if you are using Rancher Prime
    rancherChartVersion: ""                       # REQUIRED - fill with desired value
    rancherChartRepository: ""                    # REQUIRED - fill with desired value. Must end with a trailing /
    rancherHostname: ""                           # REQUIRED - fill with desired value
    rancherImage: ""                              # REQUIRED - fill with desired value
    rancherTagVersion: ""                         # REQUIRED - fill with desired value
    registryPassword: ""                          # REQUIRED
    registryUsername: ""                          # REQUIRED
    repo: ""                                      # REQUIRED - fill with desired value
    rke2Group: ""                                 # REQUIRED - fill with group of the instance created
    rke2User: ""                                  # REQUIRED - fill with username of the instance created
    rke2Version: ""                               # REQUIRED - fill with desired RKE2 k8s value (i.e. v1.35.6)
    upgradedRancherAgentImage: ""                 # OPTIONAL - fill out if you are performing an upgrade
    upgradedRancherChartRepository: ""            # OPTIONAL - fill out if you are performing an upgrade
    upgradedRancherChartVersion: ""               # OPTIONAL - fill out if you are performing an upgrade
    upgradedRancherImage: ""                      # OPTIONAL - fill out if you are performing an upgrade
    upgradedRancherRepo: ""                       # OPTIONAL - fill out if you are performing an upgrade
    upgradedRancherTagVersion: ""                 # OPTIONAL - fill out if you are performing an upgrade
terratest:  
  pathToRepo: "go/src/github.com/rancher/tests"
```

## Test Cases

### Prime UI test

#### Description:
Verifies various Prime UI settings.

#### Table Tests:
1. `Prime_Local_Cluster_Rancher_Images`
2. `Prime_Brand`
3. `Prime_SCC_Registration`
4. `Prime_System_Default_Registry`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/postrelease --junitfile results.xml --jsonfile results.json -- -tags=prime -run TestPostReleasePrimeUITestSuite -timeout=1h -v`

### Post Release Rancher - Fresh install

#### Description:
Installs the newly released Rancher as a fresh install.

#### Table Tests:
1. `Post_Release`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/postrelease --junitfile results.xml --jsonfile results.json -- -tags=postrelease -run TestPostReleaseTestSuite -timeout=1h -v`

### Post Release Rancher - Upgrade

#### Description:
Installs the previously released Rancher version and then upgrades to the newest released Rancher version.

#### Table Tests:
1. `Post_Release_Upgrade`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/postrelease --junitfile results.xml --jsonfile results.json -- -tags=postrelease -run TestPostReleaseUpgradeTestSuite -timeout=1h -v`

## Additional
1. If the tests passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
2. All of the tests utilize parallelism when running for more finite control of how things are run in parallel use the -p and -parallel.