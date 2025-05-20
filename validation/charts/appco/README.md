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

In your env vars, set the following:

```bash
set APPCO_USERNAME="<appco-username>"
set APPCO_ACCESS_TOKEN="<appco-access-token>"
```
