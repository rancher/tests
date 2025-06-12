# RANCHERINT Schemas

## Test Suite: Harvester

### Import a Harvester Setup into Rancher

TestHarvesterTestSuite/TestImport

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  * harvester setup available in same subnet as your rancher setup   |                                                                                                                                                       |
| 2           | Create a 'virtualization cluster' in rancher     | generates an import-cluster name in rancher   | `pending` import on virtualization page appears; rancher creates an import-cluster object with correct annotations for harvester registration to occur  |
| 3           | Set registration URL in harvester settings | uses reg. command from previous step | harvester cluster begins registration process with rancher, and gets to an `active` state |


### Node Driver Cluster Provisioning

TestRKE2ProvisioningTestSuite TestK3SProvisioningTestSuite TestRKE1ProvisioningTestSuite

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  * harvester setup imported into rancher   |                                                                                                                                                       |
| 2           | create a downstream cluster using harvester node driver     | /validation/harvester/schemas/permutation_options.md | cluster comes to an `active` state  |


### Node Driver Cloud Provider RKE2 - Loadbalancing and Storage

TestRKE2ProvisioningTestSuite TestK3SProvisioningTestSuite TestRKE1ProvisioningTestSuite

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  * downstream cluster in rancher with harvester cloud provider enabled | these tests are ran automatically when the cloud provider is enabled as part of provisioning |
| 2           | attach a LB to a workload/deployment     | /validation/harvester/schemas/workload_network.md | IP:PORT of the loadbalancer should result in a 200 status  |
| 3           | attach a PV to a workload/deployment     | /validation/harvester/schemas/workload_storage.md | writing and reading file(s) in the mountpoint are successful |


### Custom Cluster Provisioning

TestCustomClusterRKE2ProvisioningTestSuite TestCustomClusterK3SProvisioningTestSuite TestCustomClusterRKE1ProvisioningTestSuite

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  * harvester setup imported into rancher   |                                                                                                                                                       |
| 2           | create VM(s) in harvester    | * deploy VMs directly through harvester\n  * make sure you have ssh access and know the user for the VMs you're deploying  | able to ssh into each node and see that it is healthy  |
| 3           | create a custom cluster in rancher | /validation/harvester/schemas/permutation_options.md | custom cluster comes to `pending` state in rancher |
| 4           | register VM(s) with custom cluster | * for each VM, select the appropriate role(s) in rancher UI, enable/disable certificate checking (based on the http certs installed on rancher server)\n * copy the registration command\n * ssh into the appropriate VM and run the copied command | all nodes come to an active state, and the entire cluster updates to `active` once all nodes are active in the cluster |


### Scale Pools on a Downstream Cluster

TestNodeScalingTestSuite/TestScalingNodePools

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  Downstream cluster deployed in rancher (using harvester VMs) with dedicated node-per-role pools (i.e. 1 pool for etcd, one for cp, and one for worker)  |                                                                                                                                                       |
| 2           | Scale each node role on the cluster     |  one at a time, scale up, then down, each pool in the cluster.  | all pools are scaled appropriately  |


### Public Fleet Git Repo

TestFleetPublicRepoTestSuite/TestGitRepoDeployment

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  Downstream cluster deployed in rancher (using harvester VMs)  |                                                                                                                                                       |
| 2           | Deploy resources to a downstream cluster using fleet     | * add a gitRepo in fleet; select downstream cluster(s) to deploy the resources to  | fleet resources are active in UI. Downstream cluster shows new resources as active  |


### Snapshot & Restore of a Downstream Cluster

TestSnapshotRestoreUpgradeStrategyTestSuite

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  Downstream cluster deployed in rancher (using harvester VMs)  |                                                                                                                                                       |
| 2           | Take a snapshot     | Configure options (upgrade strategy, s3, etc.) and take the snapshot  | snapshot completes for all etcd nodes and shows as active in the appropriate store / locally  |
| 3           | Restore a snapshot     | Configure options (upgrade strategy, s3, etc.) and take the snapshot  | snapshot completes for all etcd nodes and shows as active in the appropriate store / locally  |


### Certificate Rotation of a Downstream Cluster

TestCertRotationTestSuite

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  Downstream cluster deployed in rancher (using harvester VMs)  |                                                                                                                                                       |
| 2           | Rotate Certificates on the Cluster     |  rotate all certificates on the cluster | all certificates are rotated (have new expiration dates)  |


### Imported Cluster from Harvester VMs

TBD

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  * harvester setup imported into rancher   |                                                                                                                                                       |
| 2           | create VM(s) in harvester    | * deploy VMs directly through harvester  | able to ssh into each node and see that it is healthy  |
| 3           | create a standalone cluster |  /validation/harvester/schemas/distro_permutation_options.md | standalone cluster comes to shows all nodes as registered with `kubectl get nodes` and all workloads are healthy |
| 4           | register imported cluster with rancher | * create an imported cluster object in rancher\n * copy the import cluster registration command\n * in a shell environment where you have access to the standalone cluster, run the registration command | the import cluster object in rancher updates to `active` |


### Install UI Extension

TBD

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  preferred for automation purposes to already have a harvester setup imported into rancher, however this is not the default/happy path from a customer point of view   |                                                                                                                                                       |
| 2           | Install the UI extension     | chart is pre-packaged in rancher. Should be nearly as simple as installing a helm chart   | harvester UI available for admin user  |


### Cloud Credential Rotation

TBD

| Step Number | Action                             | Data                                                   | Expected Result                                                                                                                                       |
| ----------- | ---------------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1           | Prerequisites            |  harvester setup imported into rancher  |                                                                                                                                                       |
| 2           | Rotate a Cloud Credential    | /validation/harvester/schemas/cloudcredential.md   | cloud credential is usable with a new expiration date  |

