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
schemaupload searches for yaml files within a "schemas" folder on any package. These files should be named "[team_name or feature]_schemas.md" and use yaml keys that correspond to values within Qase. See below example for the structure and some common field values:

```yaml
- projects: [RRT, RM] # Normally this will probably be one project, but this can upload to multiple Qase projects as defined here.
  suite: Go Automation/Certificates # Use slashes to define sub-suites if relevant
  cases:
  - title: "Example Cert Test 1"
    description: "Some example description."
    automation: 2 # Valid values: 0 (Not Automated) or 2 (Automated)
    steps:
    - action: "some action 1" # Action is a required field on a step. It cannot be empty.
      data: "/relative/path/to/file" # Path to file within current directory in case there is a lot of data that is better read as its own file. Otherwise just text is fine here.
      expectedresult: ""
      position: 1 # This is called "position" in Qase, but is really just the step number. This is a required field, and must increment each step.
    - action: "Some action 2"
      data: ""
      expectedresult: "final expectation"
      position: 2
    custom_field:
      # There are a few custom fields in Qase that can be used. See below examples for some common ones. This is ID-based. Comments below show the true name of the custom field.
      "14": "Validation" # TestSource
      "15": "TestCertificateTestSuite/TestCertExample" # AutomationTestName
      "18": "Hostbusters" # Owner
  - title: "Example test 2"
    description: ""
    automation: 0
    priority: 0 # 0: Not set, 1: High, 2: Medium, 3: Low, 4: P0, 5: P1, 6: P2
    type: 0 # 1: Other, 2: Smoke, 3: Regression, 4: Security, 5: Usability, 6: Performance, 7: Acceptance, 8: Functional, 9: Compatibility, 10: Integration, 11: Exploratory
    is_flaky: 0 # 0: No, 1: yes
    steps:
    - action: "required action field"
      data: "* first line
        * second line
        * third line
        * These will unfortunately still all only be one line in Qase, so better to use a file with multiple lines for this functionality"
      expectedresult: ""
      position: 1
```

Please note that for table tests, they can be included either as test case steps within one test case or as individual unique test cases, whichever is clearer.

## Test Run
Test run is primarily used to create a test run for our different recurring run pipelines ie daily, weekly and biweekly. There is a custom field in test run for source, so we can filter by how the test run is created.