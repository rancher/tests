## Active Directory Authentication Tests

This package contains tests for Active Directory authentication provider functionality in Rancher.

## Table of Contents

- [Test Coverage](#test-coverage)
- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
    - [Rancher Configuration](#rancher-configuration)
    - [Active Directory Test Configuration](#active-directory-test-configuration)
    - [Group Hierarchy](#group-hierarchy)
    - [Running the Tests](#running-the-tests)

## Test Coverage

These tests validate:

- Authentication provider enable/disable functionality
- User authentication with different access modes (unrestricted, restricted, required)
- Group membership and nested group inheritance
- Cluster and project role bindings with AD groups
- Access control for authorized and unauthorized users

## Prerequisites

- Active Directory must be configured in your Rancher instance
- AD server must have nested group support enabled
- Test users and groups must exist in your AD directory with the following hierarchy:
  ```
  nestgroup1 (doubleNestedGroup)
    └─ nestgroup2 (nestedGroup)
        └─ testautogroup1 (group)
  ```

## Configuration

### Rancher Configuration

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  clusterName: "cluster_to_run_tests_on"
  insecure: true
  cleanup: false
```

### Active Directory Test Configuration

Add the test user and group mappings under activeDirectoryAuthInput:

```yaml
activeDirectory:
  hostname: "active_directory_host"
  port: 389
  tls: false
  startTLS: false
  users:
    searchBase: "OU=ad-test,DC=qa-adserver-ad,DC=ad,DC=com"
    objectClass: "person"
    usernameAttribute: "name"
    loginAttribute: "sAMAccountName"
    searchAttribute: "sAMAccountName|sn|givenName"
    enabledAttribute: "userAccountControl"
    disabledBitMask: 2
    admin:
      username: "<admin-username>"
      password: "<admin-user-password>"
  serviceAccount:
    distinguishedName: "ad\\<service-account>"
    password: "<service-account-password>"
  groups:
    searchBase: "OU=ad-test,DC=qa-adserver-ad,DC=ad,DC=com"
    objectClass: "group"
    nameAttribute: "name"
    searchAttribute: "sAMAccountName"
    memberMappingAttribute: "member"
    memberUserAttribute: "distinguishedName"
    dnAttribute: "distinguishedName"
    nestedGroupMembershipEnabled: true
  accessMode: "unrestricted"

activeDirectoryAuthInput:
  standardUser: "<standard-user>"
  group: "<group-name>"
  users:
    - username: "<username1>"
      password: "<password1>"
    - username: "<username2>"
      password: "<password2>"
  nestedGroup: "<nested-group-name>"
  nestedUsers:
    - username: "<nested-username1>"
      password: "<nested-password1>"
  doubleNestedGroup: "<double-nested-group-name>"
  doubleNestedUsers:
    - username: "<double-nested-username1>"
      password: "<double-nested-password1>"
  tripleNestedGroup: "<triple-nested-group-name>"
  tripleNestedUsers:
    - username: "<triple-nested-username1>"
      password: "<triple-nested-password1>"
```

### Group Hierarchy

- `group`: Base group containing direct members (username1, username2)
- `nestedGroup`: Child group one level deep (nested-username1)
- `doubleNestedGroup`: Parent group two levels deep (double-nested-username1)
- `tripleNestedGroup`: Grandparent group three levels deep (triple-nested-username1)

### Running the Tests

**Run Active Directory Authentication Tests**

Your GO suite should be set to `-run ^TestActiveDirectoryAuthProviderSuite$`

**Example:**

```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/activedirectory --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestActiveDirectoryAuthProviderSuite$
```
