# SCIM Test Suite

This package contains Golang automation tests for Rancher's SCIM 2.0 integration.

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.
- Ensure OpenLDAP is configured in your Rancher instance. For full OpenLDAP configuration details see the [OpenLDAP auth provider README](../provider/openldap/README.md).
- Ensure at least two OpenLDAP users are listed under `openLdapAuthInput`, because the login suite signs both of them in.

## Build Types

`scim` is a Prime feature, so a community Rancher server answers every SCIM endpoint with 404. The suites read
the build type from the server and run what applies to it: the Prime feature suite runs on both and picks the
Prime or the community case per test, while the functional suites skip on a community server and say why.

Set `scimInput.buildType` to the build you expect, and the suites fail instead of skipping when the server
reports the other one. Leave it unset to run against whatever the server is.

## What Setup Creates

Each suite prepares what it needs and puts the server back as it found it:

- **The auth config of the provider**, created when the Rancher server holds none. A v2.16 server seeds only
  the `local` auth config and drops the provider's one when it is disabled, so each run creates it again.
- **The SCIM token secret and the `scim-config-<provider>` ConfigMap**, created for the provider when they are missing.
- **The audit log**, turned on by the audit suite at level 2 with the `rancher-audit-log` sidecar. This restarts Rancher, and the deployment is restored when the suite finishes.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the scim_openldap_test.go, set the GO suite to `-run ^TestSCIMOpenLDAPSuite$`
- To run the scim_prime_test.go, set the GO suite to `-run ^TestSCIMPrimeFeatureSuite$`
- To run the scim_audit_test.go, set the GO suite to `-run ^TestSCIMAuditSuite$`
- To run the scim_login_test.go, set the GO suite to `-run ^TestSCIMLoginAttributesSuite$`
- To run the scim_security_test.go, set the GO suite to `-run ^TestSCIMSecuritySuite$`

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

scimInput:
  buildType: "prime"
```
