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
- [Terraform Tests](#terraform-tests)
  - [Terraform Prerequisites](#terraform-prerequisites)
  - [Terraform Configuration](#terraform-configuration)
  - [Provider Version](#provider-version)
  - [Running the Terraform Tests](#running-the-terraform-tests)

## Test Coverage

These tests validate:

- Authentication provider enable/disable functionality
- User authentication with different access modes (unrestricted, restricted, required)
- Group membership and nested group inheritance
- Cluster and project role bindings with LDAP groups
- Access control for authorized and unauthorized users
- Enabling, disabling and destroying the provider through the `rancher/rancher2` Terraform provider,
  with the resulting state validated over the Rancher API

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
```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/openldap --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestOpenLDAPAuthProviderSuite$
```

## Terraform Tests

`TestOpenLDAPTerraformSuite` enables, disables and destroys OpenLDAP through the
`rancher/rancher2` Terraform provider and validates the resulting state over the Rancher API. It is
a separate suite so that `TestOpenLDAPAuthProviderSuite` stays runnable without a `terraform`
binary.

### Terraform Prerequisites

- A binary named `terraform` on `PATH`. terratest invokes `terraform` by name, so OpenTofu only
  works through a `terraform` symlink — which is what `Dockerfile.validation` and `Dockerfile.e2e`
  install
- Everything listed under [Prerequisites](#prerequisites) and
  [Configuration](#configuration) above

### Terraform Configuration

The Terraform work is done by `tests/extensions/authprovider` in tfp-automation; this suite only
drives it and then asserts over the Rancher API. The directory settings come from the `openLDAP`
block above, so no credentials are duplicated.

The Terraform workspace needs no checkout and no configuration. `modules/rancher2` is an empty
scratch directory — `main.tf` is generated at run time and the state files are written by
Terraform — so the suite creates it at `$GOPATH/openldap/modules/rancher2` itself. The `openldap`
segment keeps the workspace off the one `TestActiveDirectoryTerraformSuite` uses, so the two can run
side by side.

The `rancher` block from [Configuration](#rancher-configuration) does have to carry `insecure` and
`adminPassword`. Both are read while the provider block is generated — `insecure` is dereferenced
without a default, and an admin token is minted from `adminPassword` — so unlike
`TestOpenLDAPAuthProviderSuite`, this suite cannot run with either one absent.

The `terraform` block is optional and only needed to pin a provider version:

```yaml
terraform:
  providerVersion: ""
  providerVersions:
    "2.15": "15.1.2"
    "2.16": "16.0.0"
```

### Provider Version

`RANCHER2_PROVIDER_VERSION` is **not required**. When nothing is pinned, the version is resolved
from the Rancher under test. First match wins:

1. `RANCHER2_PROVIDER_VERSION`, without the leading `v`
2. `terraform.providerVersion`
3. `terraform.providerVersions[<rancher major.minor>]`, keyed off the `server-version` setting
4. resolved automatically: the Rancher minor is mapped onto the provider major that tracks it, and
   the newest installable build on that major is chosen

"Installable" means a `filesystem_mirror` when `TF_CLI_CONFIG_FILE` or `~/.terraformrc` declares one
for `rancher/rancher2`, and the Terraform registry otherwise. The mirror is preferred because a CLI
config that excludes the provider from `direct` makes the registry listing misleading — the newest
published version is not necessarily one that can be installed.

Pin a version when a run must target a specific release candidate:

```bash
export RANCHER2_PROVIDER_VERSION=15.1.2-rc.3
```

A version containing `-rc` is sourced from `terraform.local/local/rancher2` rather than
`rancher/rancher2`, so the build has to be staged locally; anything else comes from the registry or
the mirror. Unlike earlier revisions of this suite, `~/.terraformrc` is respected rather than
overridden, so a mirror configured there is used as-is.

### Running the Terraform Tests

Your GO suite should be set to -run ^TestOpenLDAPTerraformSuite$

**Example:**
```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/openldap --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestOpenLDAPTerraformSuite$
```