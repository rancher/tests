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

---

## Test Suite: Demo

### Example test case

This is an example description highlighting that you can include whatever you want in this raw text. It should be a fairly short description though and/or the associated automated test case.

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Example first action | Example data |                                |
| 2           | Example final action |              | Example thing to validate here |
