# RANCHERINT Schemas

## Test Suite: Longhorn

### Fresh Longhorn Installation via Rancher App Catalog

TBD Verify Longhorn can be successfully installed through Rancher's app catalog with default settings on a clean Kubernetes cluster.

| Step Number | Action                                            | Data | Expected Result                                             |
| ----------- | ------------------------------------------------- | ---- | ----------------------------------------------------------- |
| 1           | Log into Rancher UI with cluster admin privileges |      | Rancher dashboard loads successfully                        |
| 2           | Navigate to target cluster in Rancher dashboard   |      | Cluster view displays                                       |
| 3           | Click "Apps" in the left navigation menu          |      | Apps section opens                                          |
| 4           | Click "Charts" to access the application catalog  |      | Charts catalog displays                                     |
| 5           | Search for "Longhorn" in the search bar           |      | Longhorn chart appears in results                           |
| 6           | Select the Longhorn chart from the results        |      | Longhorn installation page opens                            |
| 7           | Review default settings and click "Install"       |      | Installation begins                                         |
| 8           | Monitor installation progress in the Apps section |      | Installation completes within 10 minutes                    |
| 9           | Verify all Longhorn pods are in Running state     |      | All pods show "Running" status in longhorn-system namespace |
| 10          | Check Longhorn UI accessibility through Rancher   |      | Longhorn UI opens when clicking Longhorn button             |
| 11          | Verify default storage class creation             |      | `longhorn` storage class exists and is available            |

### Longhorn Installation with Custom Configuration

TBD Test Longhorn installation with custom settings including replica count, data path, and node selection through Rancher interface.

| Step Number | Action                                                              | Data | Expected Result                              |
| ----------- | ------------------------------------------------------------------- | ---- | -------------------------------------------- |
| 1           | Access Longhorn installation chart in Rancher Apps                  |      | Longhorn installation page displays          |
| 2           | Click "Longhorn Default Settings" to expand configuration           |      | Configuration options become visible         |
| 3           | Set Default replica count to `2`                                    |      | Setting accepts value of 2                   |
| 4           | Set Default data path to `/var/lib/longhorn-custom`                 |      | Custom path is configured                    |
| 5           | Set Storage over-provisioning percentage to `150`                   |      | Over-provisioning set to 150%                |
| 6           | Enable "Create default disk only on labeled node"                   |      | Option is enabled                            |
| 7           | Label worker nodes with `node.longhorn.io/create-default-disk=true` |      | Nodes are properly labeled                   |
| 8           | Complete installation with custom configuration                     |      | Installation succeeds with custom settings   |
| 9           | Verify custom settings in Longhorn UI                               |      | Settings reflect custom configuration        |
| 10          | Create test volume and verify replica count                         |      | Volume created with 2 replicas               |
| 11          | Check custom data path utilization on nodes                         |      | `/var/lib/longhorn-custom` directory is used |

### Longhorn UI Access Through Rancher

TBD Verify seamless access to Longhorn UI through Rancher interface with proper authentication and session management.

| Step Number | Action                                              | Data | Expected Result                            |
| ----------- | --------------------------------------------------- | ---- | ------------------------------------------ |
| 1           | Navigate to cluster view in Rancher dashboard       |      | Cluster overview displays                  |
| 2           | Verify "Longhorn" appears in left navigation menu   |      | Longhorn menu item is visible              |
| 3           | Click "Longhorn" menu item                          |      | Longhorn resources display in Rancher      |
| 4           | Click the "Longhorn" button in the Overview section |      | Longhorn UI opens in new tab/window        |
| 5           | Navigate to "Volume" section in Longhorn UI         |      | Volume management page loads               |
| 6           | Navigate to "Node" section in Longhorn UI           |      | Node management page loads                 |
| 7           | Navigate to "Setting" section in Longhorn UI        |      | Settings page loads                        |
| 8           | Return to Rancher tab and verify session maintained |      | Rancher session still active               |
| 9           | Create test volume from Longhorn UI                 |      | Volume creation succeeds                   |
| 10          | Return to Rancher and verify volume appears         |      | Volume visible in Rancher Longhorn section |

### RBAC Integration Testing

TBD Test Role-Based Access Control integration between Rancher and Longhorn for different user permission levels.

| Step Number | Action                                             | Data | Expected Result                                  |
| ----------- | -------------------------------------------------- | ---- | ------------------------------------------------ |
| 1           | Create cluster admin user in Rancher               |      | User created with cluster-admin role             |
| 2           | Create project member user in Rancher              |      | User created with project-member role            |
| 3           | Create read-only user in Rancher                   |      | User created with read-only permissions          |
| 4           | Login as cluster admin and access Longhorn UI      |      | Full access to all Longhorn features             |
| 5           | Test volume creation and deletion as cluster admin |      | Operations succeed without restrictions          |
| 6           | Login as project member and access Longhorn UI     |      | Access granted with project scope                |
| 7           | Test volume operations within project scope        |      | Operations succeed within assigned project       |
| 8           | Login as read-only user and access Longhorn UI     |      | Read-only access granted                         |
| 9           | Attempt volume creation as read-only user          |      | Operation blocked with appropriate error message |
| 10          | Verify RBAC consistency across both UIs            |      | Permissions enforced consistently                |

### Volume Creation Through Rancher Workloads

TBD Verify automatic volume provisioning and attachment when deploying workloads with Longhorn storage through Rancher.

| Step Number | Action                                                                                          | Data | Expected Result                             |
| ----------- | ----------------------------------------------------------------------------------------------- | ---- | ------------------------------------------- |
| 1           | Navigate to "Workloads" in Rancher cluster view                                                 |      | Workloads section displays                  |
| 2           | Click "Create" to deploy new workload                                                           |      | Workload creation form opens                |
| 3           | Configure workload with name `test-longhorn-workload` and nginx image                           |      | Basic workload configuration set            |
| 4           | Click "Add Volume" → "Add a new persistent volume (claim)"                                      |      | Volume configuration dialog opens           |
| 5           | Configure PVC: name=`test-pvc`, storageClass=`longhorn`, size=`1Gi`, accessMode=`ReadWriteOnce` |      | PVC settings configured                     |
| 6           | Set mount path to `/data`                                                                       |      | Mount path configured                       |
| 7           | Deploy the workload                                                                             |      | Workload deployment begins                  |
| 8           | Verify workload pod reaches Running state                                                       |      | Pod shows "Running" status                  |
| 9           | Check Longhorn UI for automatic volume creation                                                 |      | Volume appears in Longhorn UI as "Attached" |
| 10          | Exec into pod and write test data to `/data`                                                    |      | Data write succeeds                         |
| 11          | Delete and recreate pod, verify data persistence                                                |      | Data persists across pod restart            |

### Create and Scale StatefulSet with PVC Template

TBD Test Longhorn integration with StatefulSet persistent volumes including scaling operations and pod rescheduling.

| Step Number | Action                                                                             | Data | Expected Result                                    |
| ----------- | ---------------------------------------------------------------------------------- | ---- | -------------------------------------------------- |
| 1           | Create StatefulSet manifest with volumeClaimTemplates using Longhorn storage class |      | Manifest prepared with Longhorn PVC template       |
| 2           | Deploy StatefulSet with 3 replicas via Rancher or kubectl                          |      | StatefulSet deployment begins                      |
| 3           | Verify each pod gets dedicated persistent volume                                   |      | 3 volumes created, each attached to respective pod |
| 4           | Write unique test data to each pod's volume                                        |      | Data successfully written to all volumes           |
| 5           | Scale StatefulSet up to 5 replicas                                                 |      | Scaling operation succeeds                         |
| 6           | Verify new pods get new dedicated volumes                                          |      | 2 additional volumes created and attached          |
| 7           | Scale StatefulSet down to 2 replicas                                               |      | Scaling down completes                             |
| 8           | Verify volumes remain available for future scaling                                 |      | Volumes persist in "Detached" state                |
| 9           | Delete one pod to trigger rescheduling                                             |      | Pod rescheduled to different node                  |
| 10          | Verify volume reattaches to rescheduled pod with data intact                       |      | Data consistency maintained                        |

### Longhorn Metrics in Rancher Monitoring Interoperability

TBD Verify Longhorn metrics collection and visualization through Rancher's Prometheus and Grafana monitoring stack.

| Step Number | Action                                                           | Data | Expected Result                                |
| ----------- | ---------------------------------------------------------------- | ---- | ---------------------------------------------- |
| 1           | Install Rancher monitoring stack from Apps catalog               |      | Monitoring deployment completes                |
| 2           | Verify Prometheus and Grafana pods are running                   |      | All monitoring components operational          |
| 3           | Access Longhorn Settings and enable "Longhorn System Monitoring" |      | Monitoring integration enabled                 |
| 4           | Access Grafana through Rancher monitoring section                |      | Grafana dashboard opens                        |
| 5           | Search for "Longhorn" dashboards in Grafana                      |      | Longhorn dashboards appear in search results   |
| 6           | Open "Longhorn - Volume" dashboard                               |      | Volume metrics dashboard displays              |
| 7           | Open "Longhorn - Node" dashboard                                 |      | Node metrics dashboard displays                |
| 8           | Verify volume health metrics display correctly                   |      | Volume status, replica count, and health shown |
| 9           | Verify storage utilization metrics display                       |      | Storage usage percentages and capacity shown   |
| 10          | Verify performance metrics (IOPS, throughput) display            |      | Performance data visible and updating          |
| 11          | Create test volume and verify new metrics appear                 |      | New volume metrics reflected in dashboard      |

### Alert Manager Integration

TBD Test Longhorn alert integration with Rancher Alert Manager for storage-related events and notifications.

| Step Number | Action                                                   | Data | Expected Result                            |
| ----------- | -------------------------------------------------------- | ---- | ------------------------------------------ |
| 1           | Verify Rancher monitoring and alerting are configured    |      | Alert Manager operational                  |
| 2           | Navigate to "Monitoring" → "Alertmanager" in Rancher     |      | Alert Manager interface accessible         |
| 3           | Verify Longhorn alert rules exist in Prometheus rules    |      | Rules for volume/node alerts present       |
| 4           | Configure notification channel (Slack/email) for testing |      | Notification channel configured            |
| 5           | Simulate volume degraded state by shutting down node     |      | Node shutdown triggers volume degradation  |
| 6           | Verify alert appears in Alert Manager within 2 minutes   |      | `LonghornVolumeStatusCritical` alert fires |
| 7           | Check notification delivery through configured channel   |      | Alert notification received                |
| 8           | Simulate storage space warning by filling disk           |      | Disk usage exceeds threshold               |
| 9           | Verify storage warning alert triggers                    |      | `LonghornDiskStorageWarning` alert fires   |
| 10          | Resolve issues and verify alert resolution               |      | Alerts automatically resolve               |
| 11          | Confirm resolution notifications sent                    |      | Resolution notifications received          |

### Backup Target Configuration

TBD Test backup target setup and configuration for S3 storage through Rancher interface.

| Step Number | Action                                                      | Data                                   | Expected Result                     |
| ----------- | ----------------------------------------------------------- | -------------------------------------- | ----------------------------------- |
| 1           | Access Longhorn Settings through Rancher → Longhorn         |                                        | Longhorn settings page displays     |
| 2           | Configure S3 backup target:                                 | `s3://test-bucket@us-west-2/longhorn/` | S3 backup target configured         |
| 3           | Create Kubernetes secret with S3 credentials                |                                        | Secret created with AWS access keys |
| 4           | Set "Backup Target Credential Secret" to created secret     |                                        | Credential secret configured        |
| 5           | Click "Test Connection" for backup target                   |                                        | Connection test passes successfully |
| 6           | Create test volume with sample data                         |                                        | Volume created and data written     |
| 7           | Navigate to Volume in Longhorn UI and click "Create Backup" |                                        | Backup creation dialog opens        |
| 8           | Create backup with name "test-backup-001"                   |                                        | Backup process initiates            |
| 9           | Monitor backup progress until completion                    |                                        | Backup completes successfully       |
| 10          | Verify backup appears in backup list                        |                                        | Backup listed with correct metadata |
| 11          | Test backup integrity verification                          |                                        | Backup integrity check passes       |

### Cross-Cluster Disaster Recovery

TBD Test disaster recovery volume functionality between separate Rancher-managed clusters with shared backup storage.

| Step Number | Action                                                             | Data                                            | Expected Result                            |
| ----------- | ------------------------------------------------------------------ | ----------------------------------------------- | ------------------------------------------ |
| 1           | Prerequiste                                                        | Configure two separate Rancher-managed clusters |                                            |
| 2           | Configure same S3 backup target on both source and target clusters |                                                 | Both clusters use shared backup storage    |
| 3           | Create volume with test data in source cluster                     |                                                 | Volume created with identifiable test data |
| 4           | Set up recurring backup schedule (every 30 minutes)                |                                                 | Recurring backup configured                |
| 5           | Wait for initial backup completion                                 |                                                 | Backup appears in source cluster           |
| 6           | Access target cluster Longhorn UI → Backup → Volume                |                                                 | Backup from source cluster visible         |
| 7           | Select backup and click "Create Disaster Recovery Volume"          |                                                 | DR volume creation dialog opens            |
| 8           | Configure DR volume with name "dr-test-volume"                     |                                                 | DR volume configuration set                |
| 9           | Create DR volume and verify "Standby" state                        |                                                 | DR volume shows as standby                 |
| 10          | Monitor incremental synchronization progress                       |                                                 | DR volume stays synchronized               |
| 11          | Simulate source cluster failure (power off nodes)                  |                                                 | Source cluster becomes unavailable         |
| 12          | Activate DR volume in target cluster                               |                                                 | DR volume activation succeeds              |
| 13          | Deploy application using activated DR volume                       |                                                 | Application successfully uses DR volume    |
| 14          | Verify data integrity matches source cluster                       |                                                 | All test data present and correct          |

### Node Drain Operation via Rancher

TBD Test graceful node drain functionality with Longhorn volumes ensuring proper replica evacuation and data integrity.

| Step Number | Action                                                     | Data | Expected Result                           |
| ----------- | ---------------------------------------------------------- | ---- | ----------------------------------------- |
| 1           | Identify node with attached Longhorn volumes               |      | Target node selected with active volumes  |
| 2           | Document current volume replica distribution               |      | Baseline replica placement recorded       |
| 3           | Navigate to "Nodes" in Rancher cluster view                |      | Nodes section displays                    |
| 4           | Select target node and click "..." → "Cordon"              |      | Node cordoned successfully                |
| 5           | Click "..." → "Drain" with grace period 300s               |      | Drain operation begins                    |
| 6           | Monitor drain progress in Rancher UI                       |      | Drain proceeds respecting PDB constraints |
| 7           | Verify Longhorn volume status remains healthy during drain |      | No volume degradation occurs              |
| 8           | Check Longhorn UI for replica evacuation                   |      | Replicas evacuate before pod termination  |
| 9           | Confirm drain completes successfully                       |      | Node shows as drained and cordoned        |
| 10          | Verify all volumes maintain healthy status                 |      | All volumes remain in "Robust" state      |
| 11          | Test uncordon operation by clicking "..." → "Uncordon"     |      | Node returns to schedulable state         |

### Node Scaling with Auto-Configuration

TBD Test cluster scaling operations with automatic Longhorn node configuration and replica distribution.

| Step Number | Action                                                            | Data | Expected Result                     |
| ----------- | ----------------------------------------------------------------- | ---- | ----------------------------------- |
| 1           | Document baseline: current node count and storage capacity        |      | Baseline metrics recorded           |
| 2           | Navigate to "Nodes" → "Add Node" in Rancher                       |      | Node addition interface opens       |
| 3           | Add 2 new nodes following Rancher registration process            |      | Nodes join cluster successfully     |
| 4           | Verify new nodes reach "Active" state in Rancher                  |      | Nodes show as ready and active      |
| 5           | Check Longhorn UI for automatic node configuration                |      | New nodes appear with default disks |
| 6           | Create new volumes to trigger replica placement                   |      | Volumes created across all nodes    |
| 7           | Verify replica distribution includes new nodes                    |      | Replicas distributed to new nodes   |
| 8           | Select node for removal and verify volume replicas on other nodes |      | Target node has movable replicas    |
| 9           | Drain selected node using Rancher drain functionality             |      | Node drained successfully           |
| 10          | Remove node from cluster                                          |      | Node removal completes              |
| 11          | Verify volume health maintained after node removal                |      | All volumes remain healthy          |

### Longhorn Upgrade via Rancher Apps

TBD Test Longhorn upgrade process through Rancher application management ensuring zero downtime and data integrity.

| Step Number | Action                                                  | Data | Expected Result                        |
| ----------- | ------------------------------------------------------- | ---- | -------------------------------------- |
| 1           | Create backup of all critical volumes before upgrade    |      | Backups completed successfully         |
| 2           | Document current Longhorn version and settings          |      | Baseline configuration recorded        |
| 3           | Navigate to "Apps" → "Installed Apps" in Rancher        |      | Installed applications list displays   |
| 4           | Locate Longhorn application and click "..." → "Upgrade" |      | Upgrade interface opens                |
| 5           | Review upgrade notes and breaking changes               |      | Upgrade requirements verified          |
| 6           | Select target version (e.g., v1.9.0)                    |      | Target version selected                |
| 7           | Review configuration and preserve custom settings       |      | Settings maintained through upgrade    |
| 8           | Click "Upgrade" to begin process                        |      | Upgrade process initiates              |
| 9           | Monitor upgrade progress in Apps section                |      | Upgrade proceeds without errors        |
| 10          | Verify all Longhorn components reach new version        |      | All pods updated to target version     |
| 11          | Test volume operations post-upgrade                     |      | Create, attach, detach operations work |
| 12          | Verify existing volumes remain functional               |      | All volumes accessible and healthy     |

### Kubernetes Cluster Upgrade Compatibility

TBD Test Kubernetes cluster upgrade through Rancher while maintaining Longhorn storage functionality.

| Step Number | Action                                                | Data | Expected Result                              |
| ----------- | ----------------------------------------------------- | ---- | -------------------------------------------- |
| 1           | Verify Longhorn compatibility with target K8s version |      | Compatibility matrix confirms support        |
| 2           | Create backup of critical data                        |      | Data backup completed                        |
| 3           | Navigate to "Cluster Management" and select cluster   |      | Cluster configuration page opens             |
| 4           | Click "Edit Config" and select new Kubernetes version |      | Target version selected                      |
| 5           | Review upgrade plan and rolling update strategy       |      | Upgrade plan shows minimal downtime          |
| 6           | Initiate cluster upgrade through Rancher              |      | Upgrade begins with control plane            |
| 7           | Monitor control plane upgrade completion              |      | Control plane updated successfully           |
| 8           | Monitor worker node upgrades (rolling updates)        |      | Nodes upgrade one by one                     |
| 9           | Keep Longhorn UI open to monitor volume status        |      | Volumes remain accessible throughout         |
| 10          | Verify no volume degradation during node upgrades     |      | All volumes maintain healthy status          |
| 11          | Test volume operations after cluster upgrade          |      | Volume creation and operations work normally |
| 12          | Verify all nodes at target Kubernetes version         |      | Cluster fully upgraded                       |

### High-Volume Concurrent Operations

TBD Test Longhorn performance under high-volume concurrent operations including volume creation, backup, and snapshot operations.

| Step Number | Action                                                   | Data | Expected Result                            |
| ----------- | -------------------------------------------------------- | ---- | ------------------------------------------ |
| 1           | Deploy automation scripts for concurrent volume creation |      | Scripts prepared for load testing          |
| 2           | Create 50 volumes concurrently via automated deployment  |      | Volume creation begins simultaneously      |
| 3           | Monitor cluster resource utilization during creation     |      | CPU, memory, and network within limits     |
| 4           | Verify all 50 volumes reach healthy state                |      | All volumes show "Robust" status           |
| 5           | Deploy applications using all 50 volumes simultaneously  |      | Applications deploy successfully           |
| 6           | Initiate backup operations on 20 volumes concurrently    |      | Backup operations begin                    |
| 7           | Monitor backup completion times and success rates        |      | All backups complete within SLA            |
| 8           | Create snapshots on 30 volumes while workloads active    |      | Snapshot creation succeeds                 |
| 9           | Monitor storage network utilization during operations    |      | Network usage remains stable               |
| 10          | Test volume expansion on 25 volumes concurrently         |      | Expansion operations complete successfully |
| 11          | Verify UI responsiveness during high-volume operations   |      | Longhorn UI remains responsive             |

### Large-Scale Volume Management

TBD Test Longhorn scalability with hundreds of volumes and verify system stability and UI performance.

| Step Number | Action                                                 | Data | Expected Result                           |
| ----------- | ------------------------------------------------------ | ---- | ----------------------------------------- |
| 1           | Prepare automation for creating 200+ volumes           |      | Automation scripts ready                  |
| 2           | Execute bulk volume creation to reach 200 volumes      |      | Volume creation scales successfully       |
| 3           | Deploy workloads for 150 of the created volumes        |      | Applications deploy and attach volumes    |
| 4           | Access Longhorn UI and test volume listing performance |      | UI loads volume list within 5 seconds     |
| 5           | Test volume filtering and search with large dataset    |      | Search and filter respond quickly         |
| 6           | Perform bulk snapshot creation on 100 volumes          |      | Snapshot operations complete efficiently  |
| 7           | Execute bulk backup operations on 50 volumes           |      | Backup operations scale appropriately     |
| 8           | Monitor system resource usage throughout testing       |      | Resources remain within acceptable limits |
| 9           | Test volume cleanup operations at scale                |      | Bulk deletion operations succeed          |
| 10          | Verify system stability after scale testing            |      | Cluster returns to stable baseline        |
| 11          | Confirm no orphaned resources remain                   |      | All test resources properly cleaned up    |

### Network Partition Recovery

TBD Test Longhorn behavior and automatic recovery during network partition scenarios affecting volume replicas.

| Step Number | Action                                                          | Data | Expected Result                                   |
| ----------- | --------------------------------------------------------------- | ---- | ------------------------------------------------- |
| 1           | Document baseline volume status and replica distribution        |      | All volumes healthy with proper replica placement |
| 2           | Create network partition isolating 2 nodes with volume replicas |      | Network partition established                     |
| 3           | Monitor immediate impact on volume status in Longhorn UI        |      | Some volumes show degraded status                 |
| 4           | Test read/write operations on accessible replicas               |      | Operations continue on available replicas         |
| 5           | Verify controller behavior during partition                     |      | Controllers maintain operation where possible     |
| 6           | Monitor automatic replica scheduling decisions                  |      | System handles partition intelligently            |
| 7           | Restore network connectivity between partitioned nodes          |      | Network partition removed                         |
| 8           | Monitor automatic recovery process                              |      | Recovery begins automatically                     |
| 9           | Verify replica synchronization completes                        |      | Replicas sync and volume health restores          |
| 10          | Test data consistency across all replicas                       |      | Data remains consistent across all replicas       |
| 11          | Confirm all volumes return to healthy state                     |      | All volumes show "Robust" status                  |

### Storage Node Complete Failure

TBD Test comprehensive recovery from complete storage node failure including replica rebuilding and data integrity preservation.

| Step Number | Action                                                             | Data | Expected Result                                  |
| ----------- | ------------------------------------------------------------------ | ---- | ------------------------------------------------ |
| 1           | Create test data and document checksums for integrity verification |      | Test data prepared with verification hashes      |
| 2           | Verify volumes have replicas distributed across multiple nodes     |      | Replica distribution confirmed                   |
| 3           | Simulate complete node failure by powering off target node         |      | Node becomes completely unavailable              |
| 4           | Monitor volume status change to degraded in Longhorn UI            |      | Affected volumes show "Degraded" status          |
| 5           | Verify volume accessibility maintained during failure              |      | Volumes remain accessible via healthy replicas   |
| 6           | Monitor automatic replica rebuilding initiation                    |      | Rebuilding begins automatically within 5 minutes |
| 7           | Track replica rebuild progress and completion                      |      | Rebuild completes successfully                   |
| 8           | Add replacement node to cluster via Rancher                        |      | New node joins cluster                           |
| 9           | Verify Longhorn auto-configures replacement node                   |      | Node configured with default storage             |
| 10          | Test volume operations during and after recovery                   |      | All operations function normally                 |
| 11          | Validate test data integrity using checksums                       |      | Data integrity preserved throughout failure      |
| 12          | Confirm all volumes return to "Robust" status                      |      | Full recovery achieved                           |

### Volume Encryption Integration

TBD Test encrypted volume functionality with proper key management and data protection through Rancher interface.

| Step Number | Action                                                                 | Data | Expected Result                         |
| ----------- | ---------------------------------------------------------------------- | ---- | --------------------------------------- |
| 1           | Create encryption secret with AES-256 key in longhorn-system namespace |      | Encryption secret created successfully  |
| 2           | Create encrypted storage class with encryption parameters              |      | Storage class configured for encryption |
| 3           | Deploy workload using encrypted storage class through Rancher          |      | Workload uses encrypted volume          |
| 4           | Verify volume shows as encrypted in Longhorn UI                        |      | Volume displays encryption status       |
| 5           | Write test data to encrypted volume                                    |      | Data write operations succeed           |
| 6           | Access storage backend directly to verify encryption at rest           |      | Data encrypted on disk                  |
| 7           | Create backup of encrypted volume                                      |      | Backup process handles encryption       |
| 8           | Verify backup data is encrypted in backup target                       |      | Backup maintains encryption             |
| 9           | Test encrypted volume restore process                                  |      | Restore succeeds with proper decryption |
| 10          | Test key rotation by updating encryption secret                        |      | New volumes use updated key             |
| 11          | Verify mixed encryption scenarios work correctly                       |      | Old and new keys coexist properly       |

### Network Security and MTLS

TBD Test Longhorn network security features including MTLS configuration and network policy enforcement.

| Step Number | Action                                                   | Data | Expected Result                         |
| ----------- | -------------------------------------------------------- | ---- | --------------------------------------- |
| 1           | Configure TLS certificates for Longhorn services         |      | Certificates configured properly        |
| 2           | Enable "Longhorn Internal MTLS" setting in Longhorn UI   |      | MTLS enabled without service disruption |
| 3           | Create NetworkPolicy to restrict Longhorn traffic        |      | Network policies applied                |
| 4           | Test volume operations with MTLS enabled                 |      | All operations function normally        |
| 5           | Use network monitoring to verify encrypted communication |      | All traffic encrypted                   |
| 6           | Test certificate rotation using cert-manager             |      | Rotation handled gracefully             |
| 7           | Verify volume operations during certificate rotation     |      | No service interruption                 |
| 8           | Run security scan on Longhorn components                 |      | Security scan passes requirements       |
| 9           | Test network policy enforcement                          |      | Unauthorized traffic blocked            |
| 10          | Verify compliance with security benchmarks               |      | All benchmarks met                      |

### Mixed Operating System Cluster Support

TBD Test Longhorn functionality in mixed Windows/Linux clusters managed by Rancher with proper OS-specific scheduling.

| Step Number | Action                                                     | Data | Expected Result                         |
| ----------- | ---------------------------------------------------------- | ---- | --------------------------------------- |
| 1           | Verify cluster contains both Windows and Linux nodes       |      | Mixed OS cluster confirmed              |
| 2           | Confirm Longhorn components scheduled only on Linux nodes  |      | Proper OS-specific scheduling           |
| 3           | Deploy Linux workload with Longhorn PVC                    |      | Linux application uses Longhorn storage |
| 4           | Deploy Windows workload on Windows nodes                   |      | Windows application schedules correctly |
| 5           | Verify Windows workloads don't attempt Longhorn attachment |      | No Longhorn scheduling on Windows       |
| 6           | Test cluster drain operations on Windows nodes             |      | Windows node drain completes            |
| 7           | Test cluster drain operations on Linux nodes with Longhorn |      | Linux node drain respects Longhorn PDBs |
| 8           | Monitor resource utilization on both node types            |      | Resources appropriate for each platform |
| 9           | Verify cluster scaling with mixed node types               |      | Scaling operations work correctly       |
| 10          | Test cross-platform data sharing scenarios                 |      | Data sharing works as designed          |

### Cloud Provider Integration

TBD Test Longhorn integration with cloud provider features including multi-storage classes and cloud-specific functionality.

| Step Number | Action                                                             | Data | Expected Result                              |
| ----------- | ------------------------------------------------------------------ | ---- | -------------------------------------------- |
| 1           | Configure cloud provider storage class (AWS EBS/Azure Disk/GCP PD) |      | Cloud storage class available                |
| 2           | Maintain Longhorn storage class alongside cloud storage            |      | Multiple storage classes coexist             |
| 3           | Deploy workloads using both storage types                          |      | Both provisioners work correctly             |
| 4           | Configure Longhorn backup target using cloud object storage        |      | Cloud backup integration successful          |
| 5           | Test backup operations to cloud storage (S3/Blob/GCS)              |      | Backups work with cloud storage              |
| 6           | Deploy cluster across multiple availability zones                  |      | Multi-AZ deployment successful               |
| 7           | Configure Longhorn replicas across zones                           |      | Cross-zone replication configured            |
| 8           | Test zone failure scenarios                                        |      | System handles zone failures gracefully      |
| 9           | Verify cross-zone data replication                                 |      | Data replicated across zones                 |
| 10          | Test cloud provider maintenance compatibility                      |      | Longhorn unaffected by cloud maintenance     |
| 11          | Compare performance across different cloud instance types          |      | Performance consistent across instance types |
