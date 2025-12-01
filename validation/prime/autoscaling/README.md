# RKE2 Provisioning Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general prime](../README.md)


## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults)

### Auto Scaling Test

#### Description: 
The Autoscaler tests verifies functionality for the prime only cluster autoscaler feature.

#### Required Configurations: 
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_Auto_Scale_Up`
2. `K3S_Auto_Scale_Up`
3. `RKE2_Auto_Scale_Down`
4. `K3S_Auto_Scale_Down`
5. `RKE2_Auto_Scale_Pause`
6. `K3S_Auto_Scale_Pause`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/prime/autoscaling --junitfile results.xml --jsonfile results.json -- -tags=prime -timeout=3h -v`


## Configurations
All configurations for these tests can be found in [defaults](defaults/defaults.yaml).


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
   level: "info" #trace debug, info, warning, error
```

## Additional
1. If the tests passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
2. All of the tests utilize parallelism when running for more finite control of how things are run in parallel use the -p and -parallel args.