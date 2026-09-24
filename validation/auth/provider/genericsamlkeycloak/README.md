## Generic SAML Authentication Tests (backed by Keycloak)

This package contains tests for Rancher's generic, configurable SAML 2.0 authentication provider
(`genericsaml`), added by [rancher/rancher#56139](https://github.com/rancher/rancher/pull/56139) to
close [rancher/rancher#50664](https://github.com/rancher/rancher/issues/50664). The suite drives the
provider against a Keycloak realm, reusing the same realm, group and user provisioning the Keycloak
SAML suite uses. A future suite backed by a different identity provider would sit alongside this one
as `genericsaml<idp>`.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [Generic SAML Test Configuration](#generic-saml-test-configuration)
  - [What Setup Creates](#what-setup-creates)
  - [Running the Tests](#running-the-tests)

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
  clusterName: "cluster_to_run_tests_on"
  insecure: true
  cleanup: false
```

### Generic SAML Test Configuration

```yaml
genericsaml: {}

keycloaksaml:
  keycloakHost: "https://keycloak_server_address"
  keycloakRealm: "rancher"
  keycloakAdminUser: "<keycloak-admin-username>"
  keycloakAdminPassword: "<keycloak-admin-password>"
  keycloakInsecure: true
```

Both keys are required, and they configure different things. `genericsaml` is the Rancher-side
provider under test — its access mode, attribute field mappings, and the
`nameIDFormat`/`signatureMethod`/`allowIdpInitiated`/`forceAuthn` fields only the generic provider
consumes — and every field defaults, so it can stay empty. `keycloaksaml` is the Keycloak admin
connection (`authactions.NewKeycloakClient`), read unconditionally regardless of which Rancher
provider is under test, since it is how the suite provisions the realm the `genericsaml` client is
registered against.

### What Setup Creates

Setup registers a **second** SAML client in the realm, distinct from the one the Keycloak SAML suite
registers, under the entity ID `https://<rancher.host>/v1-saml/genericsaml/saml/metadata`. It reuses the
Keycloak SAML suite's group and user provisioning outright: a group hierarchy (`group` → `nestedGroup` →
`doubleNestedGroup`), an administrator account, and an account outside every group. Everything created is
deleted when the suite finishes.

### Running the Tests

**Run Generic SAML Authentication Tests**
Your GO suite should be set to `-run ^TestGenericSAMLKeycloakAuthProviderSuite$`

**Example:**
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/genericsamlkeycloak --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestGenericSAMLKeycloakAuthProviderSuite$`
