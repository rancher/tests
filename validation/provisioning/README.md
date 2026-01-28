# Provisioning Configs

## Table of Contents
1. [Getting Started](#Getting-Started)
2. [Cluster Type READMEs](#Cluster-Type-READMEs)

## Getting Started
Your GO suite should be set to `-run ^Test<enter_pkg_name_here>ProvisioningTestSuite$`. You can find the correct suite name in the below README links, or by checking the test file you plan to run.
In your config file, set the following:
```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  cleanup: false
  insecure: true/optional
  cleanup: false/optional
```

## Cluster Type READMEs

From there, your config should contain the tests you want to run (provisioningInput), tokens and configuration for the provider(s) you will use, and any additional tests that you may want to run. Please use one of the following links to continue adding to your config for provisioning tests:

1. [RKE1 Provisioning](rke1/README.md)
2. [RKE2 Provisioning](rke2/README.md)
3. [K3s Provisioning](k3s/README.md)
4. [K3k Provisioning](k3k/README.md) - ⚠️ Not yet available (see README for details)
5. [Hosted Provider Provisioning](hosted/README.md)


## Permutations

Currently permutations is undergoing a migration from the old permutations to the new ones. During this migration both old and new permutations will be supported. Later there will be a PR introduced to depricate old permutations once all tests are converted to new permutations.

Configuration differences:
Both old and new permutations allow you to add a list of values for a given field and run a given test case over every item in that list. Here are the notible differences between the 2:
1. New permutations uses the cluster config directly while old permutations utilizes provisioning input. 
2. New permutations does not run test code. Instead it generates a list of config files of type map[string]any. These can then be safely unmarshalled into the cluster config object and subsequently consumed by test cases.
3. New permutations does not require the any of the fields of the struct being utilized to be lists. All permuting is handled before unmarshalling so no changes are needed to the struct.
4. New permutations utilizes the provider and nodeProvider instead of the providers and nodeProviders fields.
