# Deprecation and New Feature Tag Guide

[Deprecation Guide](#deprecation-of-a-feature-for-an-upcoming-release)

[New Feature Guide](#addition-of-a-feature-for-an-upcoming-release)

## Deprecation of a Feature for an Upcoming Release

we use //go:build tags in our test suites. The new branching strategy requires us to introduce a way to deprecate tests as well. We will be using go:build tags to deprecate tests. 
**NOTE:** All tests that are deprecated need the tag associated with the rancher version it is supported on to be included when running the test. Remember to do this step when running tests manually. 

### How To Deprecate
overall, the following steps should be followed:
1. add go:build tags for each rancher version that is both:
  * in [limited support](https://endoflife.date/rancher)
  * supports the feature

2. [Deprecate the Tests](#deprecating-tests)
3. [Deprecate the Actions](#deprecating-actions)

for an example of how this was done, see the [restrictedadmin rbac test cases](./validation/rbac/deprecated_restrictedadmin_test.go) and [restrictedadmin rbac actions ](./actions/rbac/verify.go)

#### Deprecating Tests
i.e. restricted admin has been enabled in rancher since at least 2.0.0, and the feature is being deprecated in 2.11.0 release. 

At the time of deprecation, 2.10, 2.9, and 2.8 have limited support (as of 2/15/2025). Therefore any test(s) that use restricted admin should have `&& (2.8 || 2.9 || 2.10)` go:build tags added. 

All tests will fall into the following categories:
* [a file of tests all specific to the deprecated feature](#deprecating-an-entire-test-file)
* [a subset of tests within a file are to be deprecated, while other tests in said file are not deprecated or not related to the feature](#deprecating-a-subset-of-tests-within-a-single-test-file)


##### Deprecating an entire test file
simply add go:build tags to the existing ones at the top of each file. 


##### Deprecating a subset of tests within a single test file
1. create a new file
2. move all deprecated tests to the new file
3. rename the new file's suite, appropriate for the deprecated tests
4. add go:build tags to the existing ones at the top of the new file

#### Deprecating Actions
1. if the **tests** being deprecated are spread across multiple packages, the deprecated action(s) used by said tests should be moved to a new file in the same folder of actions, named appropriately, signifying they contain deprecated actions. All functions in the new file should have `//Deprecated` in their godoc comment(s)
2. If the **tests** being deprecated are isolated to one package, the deprecated action(s) used by said tests should be moved to be a test helper within the deprecated test folder

##### Example action -> deprecated action

###### before deprecation:
actions/fleet/fleet.go

###### after deprecation:
actions/fleet/fleet.go -> contains non-deprecated functions
actions/fleet/deprecatedfleet.go -> contains deprecated functions. In this new file, rename the package from `fleet` to `deprecatedfleet`

###### using the deprecated action
import the `deprecatedfleet` package into test files that will be deprecated. 

##### Example action -> deprecate to test helper

###### before deprecation:
actions/fleet/fleet.go

###### after deprecation:
* actions/fleet/fleet.go -> contains non-deprecated functions
* validation/fleet/deprecated_fleet.go -> contains deprecated functions
  * these should now all be private functions, as they should not be imported outside of the test. Therefore, no importing necessary


## Addition of a Feature for an Upcoming Release
we use //go:build tags in our test suites. The new branching strategy requires us to introduce a way to introduce features and tests around said feature. We will be using go:build tags to add feature tests. 

### How to Add Tests for a New Feature
Tags added to new features should _not_ all versions of rancher that are in limited support and do not support the new feature.
Follow these steps:
1. add _notted_ go:build tags for each rancher version that both:

* in [limited support](https://endoflife.date/rancher)
* does _not_ support the feature

2. [Add Tests for a New Feature](#adding-tests-for-a-new-feature)

#### Adding Tests for a New Feature
i.e. clusterAgent and fleetAgent overrides were introduced in 2.7

At the time of introduction, 2.5 and 2.6 had limited support. Therefore any new test(s) for this feature should have `&& !(2.5 || 2.6)` go:build tags added. 

All tests for a new feature should have dedicated file(s) for each area they are being tested in. i.e. for clusterAgent and fleetAgent, there would be the following files added:
validaiton/provisioning/k3s/agent_test.go
validaiton/provisioning/rke2/agent_test.go
validaiton/provisioning/rke1/agent_test.go
validaiton/fleet/agent_test.go

all of which would have the _not_ tags added.

#### PIT TAGS
The pit tags organizes Go tests using **build tags**. These tags define when and how different categories of tests should be executed (daily, weekly, or event)

##### pit.daily
-  Tests that should run **every day**

##### pit.harvester.daily
-  Tests that should run **every day** on a harvester setup

##### pit.weekly
-  Tests that should run **once per week**

##### pit.event
-  Tests that should run **when a new Alpha or Release Candidate (RC) is published**