## OpenLDAP Authentication Tests

This package contains tests for OpenLDAP authentication provider functionality in Rancher.

## Table of Contents

- [OpenLDAP Authentication Tests](#openldap-authentication-tests)
- [Table of Contents](#table-of-contents)
- [Test Coverage](#test-coverage)
- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [OpenLDAP Test Configuration](#openldap-test-configuration)
  - [Group Hierarchy](#group-hierarchy)
  - [Running the Tests](#running-the-tests)

## Test Coverage

These tests validate:

- Authentication provider enable/disable functionality
- User authentication with different access modes (unrestricted, restricted, required)
- Group membership and nested group inheritance
- Cluster and project role bindings with LDAP groups
- Access control for authorized and unauthorized users

## Prerequisites

- OpenLDAP must be configured in your Rancher instance
- LDAP server must have nested group support enabled
- Test users and groups must exist in your LDAP directory with the following hierarchy:
  nestgroup1 (doubleNestedGroup)
  └─ testautogroupnested1 (nestedGroup)
  └─ testautogroup3 (group)

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

### OpenLDAP Test Configuration

Add the test user and group mappings under authInput:

```yaml
openLDAP:
  hostname: "open_ldap_host"
  insecure: true
  users:
    searchBase: "ou=users,dc=qa,dc=your_company,dc=space"
    admin:
      username: "<admin-username>"
      password: "<admin-user-password>"
  serviceAccount:
    distinguishedName: "cn=admin,dc=qa,dc=your_company,dc=space"
    password: "<Password123!>"
  groups:
    searchBase: "ou=groups,dc=qa,dc=yourcompany,dc=space"
    objectClass: "groupOfNames"
    memberMappingAttribute: "member"
    nestedGroupMembershipEnabled: true
    searchDirectGroupMemberships: true
openLdapAuthInput:
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

- ```group```: Base group containing direct members (username1, username1, username1)
- ```nestedGroup```: Child group one level deep (nested-username2, nested-username3)
- ```doubleNestedGroup```: Parent group two levels deep (nested-username1)

### Running the Tests
**Run OpenLDAP Authentication Tests**
Your GO suite should be set to -run ^TestOpenLDAPAuthProviderSuite$

**Example:**
`bashgotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/openldap --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestOpenLDAPAuthProviderSuite$`