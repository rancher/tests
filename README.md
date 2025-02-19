# Rancher Tests

Welcome to the rancher test repo. 

## Branching Strategy

main - active development branch

stable - rebased from main after each rancher/rancher release. This branch should be used when importing this repo

## Deprecation of a Feature for an Upcoming Release

we use //go:build tags in our tests suites. The new branching strategy requires us to introduce a way to deprecate tests as well. We will be using go:build tags to deprecate tests. 
**NOTE:** All tests that are deprecated need the tag associated with the rancher version it is supported on to be included when running the test. Remember to do this step when running tests manually. 

[restrictedadmin rbac test cases](./validation/rbac/deprecated_restrictedadmin_test.go)

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
1. if the **tests** being deprecated are spread across multiple packages, the deprecated action(s) used by said tests should be moved to a new file in the same folder of actions, named appropriately, signifying they contain deprecated actions
2. If the **tests** being deprecated are iscolated to one package, the deprecated action(s) used by said tests should be moved to be a test helper within the deprecated test folder

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


