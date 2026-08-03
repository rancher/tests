# OIDC Provider / OAuth2 Access Token Tests

This package contains tests for Rancher's built-in OIDC Provider and OAuth2 access token authentication introduced in v2.14.0.

## Prerequisites

- Rancher v2.14.0 or later
- An existing cluster that the user has access to
- The `server-url` setting must be set on the Rancher install. The OIDC issuer is derived from it, so an
  unset `server-url` yields a relative issuer of `/oidc` and fails RFC 8414 discovery validation.

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

`clientName` is used as a prefix; the suite appends a random suffix so repeated runs never collide.

The suite enables the `oidc-provider` feature flag during setup, which restarts the Rancher deployment at
the start of the run. Session cleanup restores the flag to whatever it was before the run and waits for the
resulting Rancher rollout to finish, so the server is left in its original state and is ready for whatever
runs next. If the flag was already enabled before the run, it is left enabled and no restart occurs at
either end.

The generated `OIDCClient` and its secret are also removed by session cleanup on teardown.

## Running the Tests

Your GO suite should be set to `-run ^TestOIDCProviderSuite$`

```bash
gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/auth/oidc \
  --junitfile results.xml \
  -- -timeout=30m -tags=validation -v -run ^TestOIDCProviderSuite$
```