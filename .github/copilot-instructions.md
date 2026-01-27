# Copilot Instructions for Test Development with Rancher/Suse Products

## Overview

This document provides guidelines for GitHub Copilot to assist with test development in the Rancher/tests test suite. The repository contains one main test framework in Golang
This framework utilizes actions, interoperability, and extensions, a collection of packages around various APIs including but not limited to Steve, Wrangler, PublicAPI.
Exentsions are located in the [shepherd repo](https://github.com/rancher/shepherd)

## Repository Structure

* **`/validation`**: End-to-end validation tests for various Rancher features (provisioning, fleet, certificates, node scaling, etc.)
* **`/actions`**: Reusable test actions and helper functions organized by feature area
* **`/interoperability`**: Tests for interoperability between different Rancher versions and components
* **`/scripts`**: Utility scripts for building, testing, and CI/CD operations
* new folder per test feature / area
* tests live in folders, whose filename always ends in `_test.go`

## Rules for function creation

* helper functions specific to a test live in a seprate file, without the `_test` filename ending
* functions that can be generalized should be, then placed in a corresponding folder in the actions/ directory
* any functions that match the extensions definition should have a PR opened to the [shepherd repo](https://github.com/rancher/shepherd) repo in the appropriate directory

## Technology Stack

* **Language**: Go 1.25.0 -- kept up to date with [upstream rancher's go.mod](https://github.com/rancher/rancher/blob/main/go.mod#L3)
* **Testing Framework**: `github.com/stretchr/testify/suite` for test suites
* **Rancher Testing Framework**: `github.com/rancher/shepherd` for Rancher-specific test utilities
* **Kubernetes Client**: k8s.io client libraries (v0.34.1) -- kept up to date with [upstream rancher's go.mod](https://github.com/rancher/rancher/blob/main/go.mod#L24-L27)
* **Linting**: golangci-lint with specific rules (see `.golangci.yaml`)

### Actions Vs. Extensions

The following qualify a function to be an extension:

* Must be either an api call not natively captured by the client OR specific CRUD / wait on resource
  * i.e. download ssh keys
  * tokens package
  * should not only directly convert -> this would make it an action

Disqualifiers for extensions (If any one or more are true, function is an action):

* if there's a custom config needed, in any part
* if it is a validation of any sort (Waits are okay)
* if function is a direct conversion of a resource
  * more on this, if the function is purely just calling a native call + catching the error, this should not be an extension. A conversion would be more complex; see an example like token package
* how re-usable is the code? Shepherd code should be highly re-usable

## Build Tags and Test Organization

### Go Build Tags

This repository uses Go build tags extensively to control which tests run in different scenarios:

1. **Feature tags**: `validation`, `infra.rke1`, `infra.rke2k3s`, `infra.aks`, `infra.eks`, `infra.gke`, etc.
2. **Cluster type tags**: `cluster.custom`, `cluster.nodedriver`, etc.
3. **Test tier tags**: `sanity`, `extended`, `stress`
4. **PIT (Platform Interoperability Testing) tags**:
   * `pit.daily`: Tests that run daily
   * `pit.harvester.daily`: Daily tests for Harvester setup
   * `pit.weekly`: Tests that run weekly
   * `pit.event`: Tests that run on Alpha/RC releases

### Version-Specific Testing

The repository uses version-specific build tags to manage feature deprecation and new feature introduction:

* **Deprecating features**: Add tags like `&& (2.8 || 2.9 || 2.10 || 2.11 || 2.12)` for features supported only in specific Rancher versions
* **New features**: Add negated tags like `&& !(2.5 || 2.6)` for features not available in older versions

See `TAG_GUIDE.md` for complete details on deprecation and new feature testing patterns.

## Code Style and Best Practices

### Linting Rules

Follow the rules defined in `.golangci.yaml`:

1. **No `time.Sleep` calls**: Use appropriate polls and watches instead
2. **No `fmt.Print*` calls**: Use testing or logrus packages for output
3. **Minimum 10 occurrences** before creating constants (goconst rule)
4. **Exported functions must have comments** (revive rule)

### Test Structure

1. **Test Suites**: Use `testify/suite` for organizing related tests
2. **Setup/Teardown**: Implement `SetupSuite` and `TearDownSuite` for test lifecycle management
3. **Test Session Management**: Use `session.NewSession()` and call `session.Cleanup()` in teardown
4. **Configuration Loading**: Use `config.LoadConfig()` to load test configurations
5. **Standard User Creation**: Tests should support running as standard (non-admin) users when appropriate

### Test File Naming

* Test files: `*_test.go`
* Deprecated tests: `deprecated_*_test.go`
* Feature-specific tests: `<feature>_test.go`
* Test Helpers: `<feature>.go`

### Actions vs Test Helpers

* **Actions** (`/actions` directory): Reusable functions shared across multiple test packages for rancher
* **Test Helpers**: Private functions within test packages for package-specific utilities

### Actions vs Interoperability

* **Actions** (`/actions` directory): Reusable functions shared across multiple test packages for rancher
* **Interoperability** (`/interoperability` directory): Reusable functions that use APIs other than rancher or shepherd

When deprecating:

* Multi-package actions → Move to `deprecated<feature>.go` in actions directory
* Single-package actions → Move to test helpers within the test package

## Running Tests

Tests can be run with specific build tags:

```bash
# Run with specific feature and version tags
go test -tags="validation,2.13" ./validation/...

# Run with specific infrastructure tags
go test -tags="validation,infra.rke2k3s" ./validation/nodescaling/rke2k3s/...
```

## Dependencies and Imports

* Uses go.mod `replace` directives for specific package versions
* Internal packages use relative imports: `github.com/rancher/tests/actions`, `github.com/rancher/tests/validation`
* External dependencies managed via go.mod/go.sum

## Branching Strategy

* **`main`**: Active branch

## CI/CD

* Uses Jenkins for CI/CD (see `Jenkinsfile`, `Jenkinsfile.e2e`, `Jenkinsfile.harvester`, etc.)
* Docker-based test execution (see `Dockerfile.validation`, `Dockerfile.e2e`)
* Tests run in containers with environment configuration

## Common Patterns

### Creating a New Test Suite

```go
//go:build validation && <feature-tags> && !<excluded-tags>

package mytest

import (
    "testing"
    "github.com/rancher/shepherd/clients/rancher"
    "github.com/rancher/shepherd/pkg/session"
    "github.com/stretchr/testify/require"
    "github.com/stretchr/testify/suite"
)

type MyTestSuite struct {
    suite.Suite
    session *session.Session
    client  *rancher.Client
}

func (s *MyTestSuite) SetupSuite() {
    testSession := session.NewSession()
    s.session = testSession
    client, err := rancher.NewClient("", testSession)
    require.NoError(s.T(), err)
    s.client = client
}

func (s *MyTestSuite) TearDownSuite() {
    s.session.Cleanup()
}

func TestMyTestSuite(t *testing.T) {
    suite.Run(t, new(MyTestSuite))
}
```

## Documentation

Always update relevant documentation when making changes:

* Update `TAG_GUIDE.md` when changing build tag patterns
* Update package-level README.md files when adding new test categories
* Add godoc comments for exported functions

## Testing Philosophy

* Tests should be independent and not rely on state from other tests
* Use appropriate timeouts and retry logic for async operations
* Clean up resources in teardown methods
* Support both admin and standard user contexts where applicable
* Use meaningful test names that describe what is being tested
* always validate in a separate function
* unless the requested test has `pit` in its tag name, only extensions or actions helpers should be used. In other words, only `pit` tests can use external APIs that are not explicitly rancher
* the featurename should only be in the test's SuiteName, not any of the individual tests
* there should be at least 2 tests per test file when possible:
  * one of which will always be contain `Dynamic` substring, which will depend on user input from "github.com/rancher/shepherd/pkg/config", wrapped in an action. See actions/fleet/fleet.go for an example
  * one of which will always be as static as possible, where no input is needed from the config
