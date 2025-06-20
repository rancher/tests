# RANCHERINT Schemas

## Test Suite: AppCo

### Install in SideCar Mode

TestSideCarInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Config a downstream cluster running in rancher |   |   |
| 2           | Create Namespace | Namespace name: istio-system |   |
| 3           | Create Secret     | Secret name: application-collection |   |
| 4           | Install Istio AppCo in SideCar mode | helm install <release-name> oci://dp.apps.rancher.io charts/istio -n istio-system --set global.imagePullSecrets={application-collection} |   |
| 5           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |

### Install in Ambient Mode

TestAmbientInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Config a downstream cluster running in rancher |   |   |
| 2           | Create Namespace | Namespace name: istio-system |   |
| 3           | Create Secret     | Secret name: application-collection |   |
| 4           | Install Istio AppCo in Ambient mode | helm install <release-name> oci://dp.apps.rancher.io charts/istio -n istio-system --set global.imagePullSecrets={application-collection} --set cni.enabled=true,ztunnel.enabled=true --set istiod.cni.enabled=true --set cni.profile=ambient,istiod.profile=ambient,ztunnel.profile=ambient |   |
| 5           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |

### Install in Standalone Mode

TestGatewayStandaloneInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Config a downstream cluster running in rancher |   |   |
| 2           | Create Namespace | Namespace name: istio-system |   |
| 3           | Create Secret     | Secret name: application-collection |   |
| 4           | Install Istio AppCo with Standalone mode | helm install <release-name> oci://dp.apps.rancher.io charts/istio -n istio-system --set global.imagePullSecrets={application-collection} --set base.enabled=false,istiod.enabled=false --set gateway.enabled=true |   |
| 5           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |

### Install with a different namespace

TestGatewayDiffNamespaceInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Config a downstream cluster running in rancher |   |   |
| 2           | Create a new Namespace | Namespace name: random-name |   |
| 3           | Create Secret     | Secret name: application-collection |   |
| 4           | Install Istio AppCo in SideCar mode with a different namespace | helm install <release-name> oci://dp.apps.rancher.io/charts/istio -n istio-system --set global.imagePullSecrets={application-collection} --set gateway.enabled=true,gateway.namespaceOverride=default |   |
| 5           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |

### Upgrade in InPlace Mode

TestInPlaceUpgrade

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Config a downstream cluster running in rancher |   |   |
| 2           | Create Namespace | Namespace name: istio-system |   |
| 3           | Create Secret     | Secret name: application-collection |   |
| 4           | Install Istio AppCo in the SideCar Mode | helm install <release-name> oci://dp.apps.rancher.io charts/istio -n istio-system --set global.imagePullSecrets={application-collection} |   |
| 5           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |
| 6           | Upgrate the Istio AppCo | helm upgrade <release-name> oci://dp.apps.rancher.io/charts/istio -n istio-system --set global.imagePullSecrets={application-collection} |   |
| 7           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |

### Upgrade with Canary

TestInstallWithCanaryUpgrade

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Config a downstream cluster running in rancher |   |   |
| 2           | Create Namespace | Namespace name: istio-system |   |
| 3           | Create Secret     | Secret name: application-collection |   |
| 4           | Install Istio AppCo in the SideCar Mode | helm install <release-name> oci://dp.apps.rancher.io charts/istio -n istio-system --set global.imagePullSecrets={application-collection} |   |
| 5           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |
| 6           | Upgrate the Istio AppCo with Canary mode | helm install <release-name> oci://dp.apps.rancher.io charts/istio -n istio-system --set global.imagePullSecrets={application-collection} |   |
| 7           | Wait for Istio Deployments be running | kubectl get pods | All the pods should be running |
