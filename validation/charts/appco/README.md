# AppCo Configs

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

In your env vars you can set the following:

```bash
APPCO_USERNAME="<appco-username>"
APPCO_ACCESS_TOKEN="<appco-access-token>"
```

# Local command example using env vars

```bash
 APPCO_USERNAME='appco-username' APPCO_ACCESS_TOKEN='appco-access-token'; go test -tags=validation -timeout 30m  -run ^TestIstioTestSuite/TestSideCarInstallation$  github.com/rancher/tests/validation/charts/appco
 ```

# Local command example using args

 ```bash
 go test -tags=validation -timeout 30m  -run ^TestIstioTestSuite/TestSideCarInstallation$  github.com/rancher/tests/validation/charts/appco -APPCO_USERNAME 'appco-username' -APPCO_ACCESS_TOKEN 'appco-access-token'
 ```