# RRT Schemas

## Test Suite: Provisioning/RKE2

### TestProvisioningRKE2Cluster

Test provisions rke2 node driver clusters with static settings

| Step Number | Action                                        | Data                                                                                                                               | Expected Result |
| ----------- | --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | --------------- |
| 1           | Create rancher provider credentials           |                                                                                                                                    |                 |
| 2           | Provision a k3s cluster                       |                                                                                                                                    |                 |
| 3           | Verify cluster state                          | validations: pods and workload provisioning                                                                                        |                 |
| 4           | Perform steps 1-3 for each node configuration | (1 etcd/cp/worker), (1 etcd/cp, 1 worker), (1 etcd, 1 cp, 1 worker), (1 etcd, 1 cp, 1 worker, 1 windows), (3 etcd, 2 cp, 3 worker) |                 |

### TestProvisioningRKE2ClusterDynamicInput

Test provisions rke2 node driver clusters with dynamic settings

| Step Number | Action                                        | Data                                        | Expected Result |
| ----------- | --------------------------------------------- | ------------------------------------------- | --------------- |
| 1           | Create rancher provider credentials           |                                             |                 |
| 2           | Provision a k3s cluster                       |                                             |                 |
| 3           | Verify cluster state                          | validations: pods and workload provisioning |                 |