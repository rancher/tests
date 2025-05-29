# Qase Reporting

The package contains three main.go files, that use the qase-go client to perform API calls to Qase. Reporter and schemaupload both update Qase with test cases and their statuses, and testrun starts and ends test runs for our recurring runs pipelines.

## Table of Contents
- [Qase Reporting](#qase-reporting)
  - [Table of Contents](#table-of-contents)
  - [Reporter](#reporter)
  - [Schema Upload](#schema-upload)
  - [Test Run](#test-run)

## Reporter
Reporter retreives all test cases inorder to determine if said automation test exists or not. If it does not it will create the test case. There is a custom field for automation test name, so we can update results for existing tests. This is to determine if a pre-existing manual test case has been automated. This value should be the package and test name, ex: TestTokenTestSuite/TestPatchTokenTest1. It will then update the status of the test case, for a specifc test run provided. 

## Schema Upload
schemaupload searches for markdown files within a "schemas" folder on any package. These files should be named "[team_name]_schemas.md" and follow a **very specific structure**:
- The top level (`#`) heading corresponds to the Qase Project Code.
- The sub-heading (`##`) is the Test Suite in Qase. There can be multiple sub-headings for different suites, separated by the common yaml separator: `\n---\n`. There is no nesting of test suites. 
- Sub-sub-headings (`###`) are the test names. 
- Any text that follows should be a short description of the test or the associated automated test, and then there should be a markdown table for the test steps. 

See /validation/fleet/schemas/... for an example. Also note that for table tests, they can be included either as test case steps within one test case or as individual unique test cases, whichever is clearer.

## Test Run
Test run is primarily used to create a test run for our different recurring run pipelines ie daily, weekly and biweekly. There is a custom field in test run for source, so we can filter by how the test run is created.