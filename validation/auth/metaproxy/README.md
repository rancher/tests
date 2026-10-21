# Meta Proxy Test Suite

This package contains Golang automation tests for the Rancher meta proxy, `/meta/proxy/<host>/<path>`, which forwards dashboard and API client requests to cloud provider APIs. The suites verify that the caller's own Rancher credentials, the session cookie and the API token, stay behind when the proxy forwards a request.

## Pre-requisites

- Ensure you have an echo host that answers with the request headers it received, as JSON under a `headers` key. The suites fail and say so when `metaProxyInput.echoHost` is not set or the host does not reflect the headers it is sent.
- Ensure the echo host is reachable from the Rancher pod. The suites add it to the `whitelist-domain` setting themselves and restore the original value afterwards, so it does not have to be allow listed beforehand.
- Prefer an echo host you control, and a throwaway admin token. The suites send the caller's own credentials through the proxy on purpose, to check that the proxy does not pass them on.

## What Setup Creates

Each suite prepares what it needs and puts the server back as it found it:

- **Probe cloud credentials**, one per signer the suite exercises, stored as secrets in `cattle-global-data` holding fake values and deleted on cleanup.
- **The `whitelist-domain` setting**, extended with the echo host and restored to its previous value on cleanup.
- **A ProxyEndpoint**, created by the allow list suite only, and deleted on cleanup.

The cookie and authorization suites read one server behaviour during setup and branch on it, because it differs by build: whether the Rancher server answers the request itself when a signer cannot resolve its credential, or logs the error and forwards the request anyway.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the metaproxy_cookie_test.go, set the GO suite to `-run ^TestMetaProxyCookieSuite$`
- To run the metaproxy_authorization_test.go, set the GO suite to `-run ^TestMetaProxyAuthorizationSuite$`
- To run the metaproxy_headers_test.go, set the GO suite to `-run ^TestMetaProxyHeaderForwardingSuite$`
- To run the metaproxy_allowlist_test.go, set the GO suite to `-run ^TestMetaProxyAllowListSuite$`

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "downstream_cluster_name"

metaProxyInput:
  echoHost: "echo_host_address"
  echoPath: "headers" #optional
  echoScheme: "https" #optional
  postPath: "post" #optional
  digestPath: "digest-auth/auth" #optional
```

`echoHost` is the only key the suites need. The values shown for the other four are the defaults the suites already apply, and they match what an httpbin style echo host serves, so a host of that shape takes no further configuration.

Set `echoScheme: http` when the echo host serves plain HTTP, and the suites address it through the proxy's `http:/<host>/<path>` form, because the meta proxy assumes HTTPS otherwise. `postPath` names the path that returns the request body under a `data` key, and `digestPath` the one that answers 401 with a digest challenge, to which the suite appends the probe credential's username and password.

## Additional

1. If the tests pass immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run. These suites read the Rancher address from the config file rather than the command line, so a rerun that changes only the config matches a cached result.
