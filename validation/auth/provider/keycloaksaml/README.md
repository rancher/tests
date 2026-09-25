## Keycloak SAML Authentication Tests

This package contains tests for Keycloak SAML authentication provider functionality in Rancher.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [Keycloak SAML Test Configuration](#keycloak-saml-test-configuration)
  - [What Setup Creates](#what-setup-creates)
  - [Running the Tests](#running-the-tests)
- [Terraform Tests](#terraform-tests)
  - [Terraform Prerequisites](#terraform-prerequisites)
  - [Terraform Configuration](#terraform-configuration)
  - [Provider Version](#provider-version)
  - [Running the Terraform Tests](#running-the-terraform-tests)

## Prerequisites

- A reachable Keycloak server and an account that can administer the target realm
- Rancher reachable at the address in `rancher.host`, because that is the address Keycloak issues assertions for

Nothing else has to exist beforehand.

## Configuration

### Rancher Configuration

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  adminPassword: "rancher_admin_password"
  clusterName: "cluster_to_run_tests_on"
  insecure: true
  cleanup: false
```

`adminPassword` is only read by [Terraform Tests](#terraform-tests), which mints an admin alias token
from it. `TestKeycloakSAMLAuthProviderSuite` runs without it.

### Keycloak SAML Test Configuration

```yaml
keycloaksaml:
  keycloakHost: "https://keycloak_server_address"
  keycloakRealm: "rancher"
  keycloakAdminUser: "<keycloak-admin-username>"
  keycloakAdminPassword: "<keycloak-admin-password>"
  keycloakInsecure: true
```

### What Setup Creates

The suite builds its own identity provider, so the realm needs nothing in it beforehand. Setup creates the following and deletes all of it when the suite finishes:

- **The realm** named by `keycloakRealm`, if it does not already exist.
- **The Rancher SAML client**, registered under the entity ID `https://<rancher.host>/v1-saml/keycloak/saml/metadata`.
- **Three protocol mappers** on that client: display name, user name and UID, and group membership.
- **The administrator account** whose SAML login enables the provider.
- **A group hierarchy**: `group` (2 members) → `nestedGroup` (1) → `doubleNestedGroup` (1), plus one account in none of them.
- **A service provider signing key pair**, generated on enable and stored on the auth config.

Members of a nested group are joined to that group alone, so a binding on the parent grants them nothing. Set `spCert` and `spKey` under the `keycloaksaml` config key to sign with your own pair instead.

### Running the Tests
**Run Keycloak SAML Authentication Tests**
Your GO suite should be set to `-run ^TestKeycloakSAMLAuthProviderSuite$`

**Example:**
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/keycloaksaml --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestKeycloakSAMLAuthProviderSuite$`

## Terraform Tests

`TestKeycloakSAMLTerraformSuite` enables, disables and destroys Keycloak SAML through the
`rancher/rancher2` Terraform provider and validates the resulting state over the Rancher API. It is
a separate suite so that `TestKeycloakSAMLAuthProviderSuite` stays runnable without a `terraform`
binary.

The disable test is skipped on
[rancher/terraform-provider-rancher2#2512](https://github.com/rancher/terraform-provider-rancher2/issues/2512):
`enabled = false` on `rancher2_auth_config_keycloak` is a no-op, because the resource PUTs the config
instead of posting the disable action the way the LDAP-family resources do. `terraform destroy` is
unaffected, so the destroy test still covers tearing the provider down.

### Terraform Prerequisites

- A binary named `terraform` on `PATH`. terratest invokes `terraform` by name, so OpenTofu only
  works through a `terraform` symlink — which is what `Dockerfile.validation` and `Dockerfile.e2e`
  install
- Everything listed under [Prerequisites](#prerequisites) and
  [Configuration](#configuration) above

### Terraform Configuration

The Terraform work is done by `tests/extensions/authprovider` in tfp-automation; this suite only
drives it and then asserts over the Rancher API. No Keycloak settings are duplicated: setup registers
the Rancher SAML client in the realm first, and the identity provider metadata, entity ID and
service provider signing pair it produces are what Terraform writes.

Because Terraform writes the auth config directly rather than going through a SAML login, the
account that enables the provider is not attached to the existing Rancher administrator the way
`TestKeycloakSAMLAuthProviderSuite` leaves it. The suite enables in `unrestricted` access mode so
that the Keycloak accounts can sign in afterwards.

The Terraform workspace needs no checkout and no configuration. `modules/rancher2` is an empty
scratch directory — `main.tf` is generated at run time and the state files are written by
Terraform — so the suite creates it at `$GOPATH/keycloaksaml/modules/rancher2` itself. The
`keycloaksaml` segment keeps the workspace off the ones `TestActiveDirectoryTerraformSuite` and
`TestOpenLDAPTerraformSuite` use, so the three can run side by side.

The `rancher` block from [Rancher Configuration](#rancher-configuration) does have to carry
`insecure` and `adminPassword`. Both are read while the provider block is generated — `insecure` is
dereferenced without a default, and an admin token is minted from `adminPassword` — so unlike
`TestKeycloakSAMLAuthProviderSuite`, this suite cannot run with either one absent.

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

### Running the Terraform Tests

Your GO suite should be set to `-run ^TestKeycloakSAMLTerraformSuite$`

**Example:**
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/keycloaksaml --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestKeycloakSAMLTerraformSuite$`
