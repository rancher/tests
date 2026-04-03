# OIDC Provider / OAuth2 Access Token Tests

This package contains tests for Rancher's built-in OIDC Provider and OAuth2 access token authentication introduced in v2.14.0.

## Prerequisites

- Rancher v2.14.0 or later
- An existing cluster that the user has access to

## Configuration

In your `cattle-config.yaml`, add the following alongside the existing `rancher:` block:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: true

oidc:
  clientName: "automation-oidc-client"
  redirectURI: "http://127.0.0.1:5556/auth/rancher/callback"
  adminUsername: "admin"
  adminPassword: "rancher_admin_password"
```

## Running the Tests

Your GO suite should be set to `-run ^TestOIDCProviderSuite$`

```bash
gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/auth/oidc \
  --junitfile results.xml \
  -- -timeout=30m -tags=validation -v -run ^TestOIDCProviderSuite$
```