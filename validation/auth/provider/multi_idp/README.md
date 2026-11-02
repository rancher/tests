## Multiple Identity Provider Tests

This package contains tests for running several auth configs of the same provider type at once in Rancher, using two OpenLDAP configs as the first case.

## Table of Contents

- [Multiple Identity Provider Tests](#multiple-identity-provider-tests)
- [Table of Contents](#table-of-contents)
- [Test Coverage](#test-coverage)
- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [Multi IdP Test Configuration](#multi-idp-test-configuration)
  - [Config Rules](#config-rules)
  - [Running the Tests](#running-the-tests)

## Test Coverage

These tests validate:

- Enabling two OpenLDAP auth configs side by side, each with its own password secret
- Login and principal IDs issued per auth config
- Denial of users from one auth config signing in through another
- UserAttribute keys scoped to each auth config
- Access mode and disabling one auth config without affecting the others
- Group cluster access and request groups on a custom named auth config
- Provider types endpoint and default config coexisting with named configs
- Principal search across auth configs when the cross-config feature is enabled

## Prerequisites

- Rancher must be running with Prime, including the `multiple-auth-providers` and `multiple-auth-provider-cross` features
- Each OpenLDAP directory must be reachable from the Rancher instance under test
- Test users and groups must exist in each directory, and the second directory needs at least one user that is not in the first

## Configuration

### Rancher Configuration

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  clusterName: "cluster_to_run_tests_on"
```

### Multi IdP Test Configuration

Add one entry per auth config under `multiIdpAuthInput`:

```yaml
multiIdpAuthInput:
  openLdap:
    - configName: "ldap-a"
      displayName: "Corp A"
      openLDAP:
        hostname: "ldap_a_host"
        port: "optional, only if the server does not use the default"
        accessMode: "unrestricted"
        serviceAccount:
          distinguishedName: "cn=admin,dc=qa,dc=your_company,dc=space"
          password: "<Password123!>"
        users:
          searchBase: "ou=users,dc=qa,dc=your_company,dc=space"
          admin:
            username: "<admin-username>"
            password: "<admin-user-password>"
        groups:
          searchBase: "ou=groups,dc=qa,dc=your_company,dc=space"
          objectClass: "groupOfNames"
          memberMappingAttribute: "member"
      directory:
        group: "<group-name>"
        users:
          - username: "<username1>"
            password: "<password1>"
    - configName: "ldap-b"
      displayName: "Corp B"
      # same shape, pointing at the second directory
```

### Config Notes

- Each `configName` must be unique and must not be `openldap`. That name belongs to the provider's default config, which the defaults tests rely on existing alongside the named configs
- The `directory` block holds the group and users the tests bind and log in with
- The second auth config needs a user that does not exist in the first directory, for the cross-config denial test

### Running the Tests

Your GO suite should be set to -run ^TestMultiOpenLDAPSuite$

**Example:**
```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/multiidp --junitfile results.xml -- -timeout=60m -tags=validation,2.16 -v -run ^TestMultiOpenLDAPSuite$
```
