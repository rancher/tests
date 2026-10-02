## Keycloak OIDC Authentication Tests

This package contains tests for Keycloak OIDC authentication provider functionality in Rancher.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
  - [Rancher Configuration](#rancher-configuration)
  - [Keycloak OIDC Test Configuration](#keycloak-oidc-test-configuration)
  - [What Setup Creates](#what-setup-creates)
  - [Running the Tests](#running-the-tests)

## Prerequisites

- A reachable Keycloak server and an account that can administer the target realm
- Rancher reachable at the address in `rancher.host`, because that is the address Keycloak redirects back to

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

### Keycloak OIDC Test Configuration

```yaml
keycloakoidc:
  keycloakHost: "https://keycloak_server_address"
  keycloakRealm: "rancher"
  keycloakAdminUser: "<keycloak-admin-username>"
  keycloakAdminPassword: "<keycloak-admin-password>"
  keycloakInsecure: true
```

Leave `pkceMethod` unset. Rancher keeps the PKCE verifier in a browser cookie that these tests never
hold, so the client refuses to sign a user in rather than failing later in the token exchange.

### What Setup Creates

The suite builds its own identity provider, so the realm needs nothing in it beforehand. Setup creates the following; the accounts and groups are deleted when the suite finishes, while the realm and the client are left in place to be reused by the next run:

- **The realm** named by `keycloakRealm`, if it does not already exist.
- **The Rancher OpenID Connect client**, registered as `rancher-oidc-automation-<rancher.host>`, confidential, with `https://<rancher.host>/verify-auth` among its redirect URIs. The host is part of the client ID so that two Rancher servers can share one realm without replacing each other's client.
- **A group membership mapper** on that client, emitting direct memberships into the `groups` claim.
- **Realm rights on that client's service account**, `view-users` and `query-groups`, which are what Rancher answers a principal search with.
- **The administrator account** whose OpenID Connect login enables the provider.
- **A group hierarchy**: `group` (2 members) → `nestedGroup` (1) → `doubleNestedGroup` (1), plus one account in none of them.
- **A client key pair bundled with the certificate Keycloak serves**, written to the auth config so Rancher trusts the connection it makes to the token endpoint.

Rancher calls Keycloak itself to exchange the authorization code, unlike the SAML providers where the
assertion travels through the browser, so it has to trust Keycloak's certificate. Setup reads that
certificate off a TLS handshake and Rancher appends it to its root pool; set `certificate` and
`privateKey` under the `keycloakoidc` config key to pin your own bundle instead. Endpoints are read
from the realm's discovery document, and the client secret is read back from Keycloak after
registration.

Members of a nested group are joined to that group alone, so a binding on the parent grants them nothing, unless the groups mapper is switched to emit `full_group_path`, which Rancher splits into one group per path segment.

### Running the Tests
**Run Keycloak OIDC Authentication Tests**
Your GO suite should be set to `-run ^TestKeycloakOIDCAuthProviderSuite$`

**Example:**
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/auth/provider/keycloakoidc --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestKeycloakOIDCAuthProviderSuite$`
