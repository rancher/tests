# Rancher Tests

Welcome to the rancher test repo.

## Provider Defaults

Package defaults are named `defaults/<provider>.yaml`, such as `aws.yaml` or
`vsphere.yaml`. Set both `clusterConfig.provider` and `terraform.provider` in
the cattle config before loading defaults; their names must contain a common
supported provider. User configuration overrides package defaults, which override
parent defaults. Hosted defaults are separated into AWS, Azure, and Google files.
Provider-independent packages such as workloads load shared defaults directly
from `defaults/defaults.yaml` without requiring provider configuration.

## Branching Strategy

main - active development branch

stable - rebased from main after each rancher/rancher release. This branch should be used when importing this repo

## Deprecation and And Adding New Feature Tests

see the [Deprecation and New Feature Tag Guide](./TAG_GUIDE.md) for more info.

## AI / Copilot Guide

see the [Copilot Guide](./COPILOT_GUIDE.md) for more info.
