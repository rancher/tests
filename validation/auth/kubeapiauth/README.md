# kube-api-auth Test Suite

This repository contains Golang automation tests for kube-api-auth, the webhook the Authorized Cluster Endpoint authenticates against.

## Pre-requisites

- Ensure you have an existing downstream RKE2 or K3s cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.
- The suites enable the Authorized Cluster Endpoint themselves and restore it afterwards, so it does not have to be enabled beforehand.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the kubeapiauth_tokenreview_test.go, set the GO suite to `-run ^TestKubeAPIAuthTokenReviewSuite$`
- To run the kubeapiauth_ace_test.go, set the GO suite to `-run ^TestKubeAPIAuthACESuite$`
- To run the kubeapiauth_authprovider_test.go, set the GO suite to `-run ^TestKubeAPIAuthProviderSuite$`

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "downstream_cluster_name"
```

The kubeapiauth_authprovider_test.go also reads the configuration of the providers it signs in through, the same keys the provider suites under validation/auth/provider use:

```yaml
openLDAP: {}
openLdapAuthInput: {}
activeDirectory: {}
activeDirectoryAuthInput: {}
keycloaksaml: {}
keycloakoidc: {}
```
