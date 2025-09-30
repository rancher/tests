
[![Go Report Card](https://goreportcard.com/badge/github.com/rancher/tests)](https://goreportcard.com/report/github.com/rancher/tests)

[![Latest Build](https://github.com/rancher/tests/actions/workflows/ci.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/ci.yml)

[![Daily Cluster Provisioning](https://github.com/rancher/tests/actions/workflows/daily-cluster-provisioning.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/daily-cluster-provisioning.yml)
[![Daily Dualstack Cluster Provisioning](https://github.com/rancher/tests/actions/workflows/daily-dualstack-cluster-provisioning.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/daily-dualstack-cluster-provisioning.yml)
[![Daily IPv6 Cluster Provisioning](https://github.com/rancher/tests/actions/workflows/daily-ipv6-cluster-provisioning.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/daily-ipv6-cluster-provisioning.yml)
[![Daily Rancher Upgrade Cluster Provisioning](https://github.com/rancher/tests/actions/workflows/daily-rancher-upgrade-cluster-provisioning.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/daily-rancher-upgrade-cluster-provisioning.yml)
[![Recurring Dualstack Tests](https://github.com/rancher/tests/actions/workflows/recurring-dualstack-tests.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/recurring-dualstack-tests.yml)
[![Recurring IPv6 Tests](https://github.com/rancher/tests/actions/workflows/recurring-ipv6-tests.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/recurring-ipv6-tests.yml)
[![Recurring Tests](https://github.com/rancher/tests/actions/workflows/recurring-tests.yml/badge.svg)](https://github.com/rancher/tests/actions/workflows/recurring-tests.yml)

# Rancher Tests

Welcome to the rancher test repo. 

## Branching Strategy

main - active development branch

stable - rebased from main after each rancher/rancher release. This branch should be used when importing this repo

## Deprecation and And Adding New Feature Tests
see the [Deprecation and New Feature Tag Guide](./TAG_GUIDE.md) for more info