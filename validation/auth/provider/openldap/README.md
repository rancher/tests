# OpenLDAP Authentication Tests

This package contains tests for OpenLDAP authentication provider functionality in Rancher.

## Table of Contents
- [OpenLDAP Authentication Tests](#openldap-authentication-tests)
  - [Table of Contents](#table-of-contents)
  - [Test Coverage](#test-coverage)
  - [Getting Started](#getting-started)
  - [Configuration](#configuration)
  - [Running the Tests](#running-the-tests)

## Test Coverage
The OpenLDAP authentication tests validate:
- Authentication provider enable/disable functionality
- User authentication with different access modes
- Group membership and nested group inheritance
- Cluster and project role bindings with LDAP groups
- Access control modes (unrestricted, restricted, cluster/project members)

## Getting Started
In your config file, set the following:
```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  clusterName: "cluster_to_run_tests_on"
  insecure: true # optional
  cleanup: false # optional
```

## Configuration
Add the OpenLDAP configuration to your config file:

```yaml
openLDAP:
  servers: ["your-ldap-server.domain.com"]
  port: 389
  tls: false
  starttls: false
  insecure: true
  connectionTimeout: 5000
  users:
    searchBase: "ou=users,dc=example,dc=com"
    admin:
      username: "admin_username"
      password: "admin_password"
    userLoginAttribute: "uid"
    userNameAttribute: "cn"
    userSearchAttribute: "uid|sn|givenName"
    userObjectClass: "inetOrgPerson"
    userMemberAttribute: "memberOf"
  serviceAccount:
    distinguishedName: "cn=admin,dc=example,dc=com"
    password: "service_account_password"
  group:
    groupSearchBase: "ou=groups,dc=example,dc=com"
    objectClass: "groupOfNames"
    memberMappingAttribute: "member"
    nestedGroupMembershipEnabled: true
    groupNameAttribute: "cn"
    groupSearchAttribute: "cn"
    groupDNAttribute: "entryDN"
    groupMemberUserAttribute: "member"
  testUsers:
    users:
      - username: "testuser1"
        password: "user_password"
    nestedUsers:
      - username: "nesteduser1"
        password: "user_password"
    doubleNestedUsers:
      - username: "doublenesteduser1"
        password: "user_password"
  testGroups:
    group: "parentgroup"
    nestedGroup: "childgroup"
    doubleNestedGroup: "grandchildgroup"
```

## Running the Tests
These tests utilize Go build tags, set the GO suite to `-run ^TestOpenLDAPAuthProviderSuite$` You can find specific tests by checking the test file you plan to run.