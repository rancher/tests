# Charts Configs

You can find the correct suite name in the below by checking the test file you plan to run.
In your config file, set the following:

```json
"rancher": { 
  "host": "<rancher-server-host>",
  "adminToken": "<rancher-admin-token>",
  "insecure": true/optional,
  "cleanup": false/optional,
  "clusterName": "<cluster-to-run-test>"
}
```

From there, please use one of the following links to check charts tests:

1. [Monitoring Chart](monitoring_test.go)
2. [Gatekeeper Chart](gatekeeper_test.go)
3. [Istio Chart](istio_test.go)
4. [Webhook Chart](webhook_test.go)
5. [Webhook Security Settings](webhook_security_settings_test.go)

## Monitoring test options

Top-level key: `monitoringTest`. All fields are optional; without any override the webhook receiver
check uses the pinned `traefik:v3.7.12` image and prefers node external addresses, falling back to
internal ones — same behavior as before except the webhook receiver image is no longer a floating
`latest` tag.

```yaml
monitoringTest:
  webhookReceiverImage: "traefik:v3.7.12"
  skipWebhookReceiver: false
  nodeAddressPreference: ["ExternalIP", "InternalIP"]
```

- `webhookReceiverImage`: overrides the webhook receiver (traefik) image, e.g. an internal mirror in
  airgap environments. When the Rancher `system-default-registry` setting is non-empty and the image
  does not already carry a registry host, the registry is prefixed automatically
  (`traefik:v3.7.12` becomes `<registry>/traefik:v3.7.12`).
- `skipWebhookReceiver`: skips the webhook receiver (alerting end-to-end) portion of the monitoring
  test; the rest of the suite still runs. Use this in airgap environments that have not mirrored the
  webhook receiver image.
- `nodeAddressPreference`: ordered node address types used to reach the webhook receiver NodePort.
  Allowed values are `ExternalIP` and `InternalIP` (default: external first, then internal). In
  private networks where nodes have no external addresses, the internal fallback keeps the check
  working.

> **Note:** how the webhook receiver accessibility check runs depends on the selected node address
> type. `ExternalIP` selections keep the direct HTTP call from the test runner (unchanged
> behavior). `InternalIP` selections are probed in-cluster instead: the test curls the receiver
> from a short-lived shell-image job created through the Rancher proxy (`kubectl.Command`), so the
> runner does not need a route into the node subnet — the position airgap CI runs from.

## Note
* For webhook charts, validations are run on the local cluster and the cluster name provided in the config.yaml. Please make sure to provide a downstream cluster name in the config.yaml instead of local cluster, so the validations are not run on the local cluster twice.