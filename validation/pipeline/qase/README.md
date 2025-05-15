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
schemaupload searches for markdown files within a "schemas" folder on any package. These files should be named "[team_name]_schemas.md" and follow a specific structure, where the top level heading corresponds to the Qase Project Code, the sub-heading from there is the Test Suite in Qase (of which there could be multiple sub-headings for different suites), and any sub-sub-headings are the test names. Any text that follows should be a short description of the test or the associated automated test, and then a markdown table for the steps. See /validation/fleet/schemas/... for an example.

## Test Run
Test run is primarily used to create a test run for our different recurring run pipelines ie daily, weekly and biweekly. There is a custom field in test run for source, so we can filter by how the test run is created.