## Keycloak SAML Authentication Tests

This package contains tests for Keycloak SAML authentication provider functionality in Rancher.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [Keycloak SAML Test Configuration](#keycloak-saml-test-configuration)
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
