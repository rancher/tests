# SCIM Test Suite

This package contains Golang automation tests for Rancher's SCIM 2.0 integration.

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.
- Ensure OpenLDAP is configured in your Rancher instance. For full OpenLDAP configuration details see the [OpenLDAP auth provider README](../provider/openldap/README.md).

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the scim_openldap_test.go, set the GO suite to `-run ^TestSCIMOpenLDAPSuite$`

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: true
  clusterName: "downstream_cluster_name"

openLDAP:
  hostname: "open_ldap_host"
  serviceAccount:
    distinguishedName: "cn=admin,dc=qa,dc=example,dc=com"
    password: "<service_account_password>"
  users:
    searchBase: "ou=users,dc=qa,dc=example,dc=com"
    admin:
      username: "<admin_username>"
      password: "<admin_password>"
  groups:
    searchBase: "ou=groups,dc=qa,dc=example,dc=com"

openLdapAuthInput:
  users:
    - username: "<username1>"
      password: "<password1>"
    - username: "<username2>"
      password: "<password2>"
```