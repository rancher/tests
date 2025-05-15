# RANCHERINT Schemas

## Test Suite: Fleet

### Test deploying fleet git repo on provisioned downstream cluster

TestGitRepoDeployment

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Create Rancher Instance            |                                                        |                                                                                                                                                       |
| 2           | Provision a kubernetes cluster     | cluster name: testcluster1                             |                                                                                                                                                       |
| 3           | Create a new project and namespace | project name: fleet-test, namespace-name: fleet-testns |                                                                                                                                                       |
| 4           | Deploy a GitRepo object            | /validation/fleet/schemas/gitrepo.yaml                 | the gitRepo itself comes to an active state and the resources defined in the spec are created on the downstream cluster in the fleet-testns namespace |
