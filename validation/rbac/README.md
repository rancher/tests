# Rbac

## Getting Started
Your GO suite should be set to `-run ^Test<>TestSuite$`. 
You can find specific tests by checking the test file you plan to run.
In your config file, set the following:

```json
"rancher": { 
  "host": "rancher_server_address",
  "adminToken": "rancher_admin_token",
  "clusterName": "cluster_to_run_tests_on",
  "insecure": true/optional,
  "cleanup": false/optional,
}
```

# RBAC input
rbacInput is needed to the run the TestRBACDynamicInput tests, specifically username, password and a cluster/project role. role takes the following as input only. Role takes a single value. 
Dynamic input will be executed on a single cluster. If the user is added to multiple downstream clusters, only the clusterName specified in the Rancher config will be taken into account. Some tests like VerifyListCluster may fail when the user is added in more than one downstream clusters.
User must be already created in the rancher server. If any other format of roles is provided, the tests fail to run:

`cluster-owner`
`cluster-member`
`project-owner`
`project-member`

```json
rbacInput:
  role: "cluster-owner"
  username: "<userID>"
  password: "<password>"
```