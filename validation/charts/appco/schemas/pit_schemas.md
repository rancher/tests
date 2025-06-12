# RANCHERINT Schemas

## Test Suite: AppCo

### Tested AppCo application installations on a provisioned downstream cluster

TestSideCarInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Create Namespace | Namespace name: istio-system ||
| 2           | Create Secret     | Secret name: application-collection ||
| 3           | Install Istio AppCo in SideCar mode |||
| 4           | Wait for Istio Deployment be done |||

---

TestAmbientInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Create Namespace | Namespace name: istio-system ||
| 2           | Create Secret     | Secret name: application-collection ||
| 3           | Install Istio AppCo in Ambient mode |||
| 4           | Wait for Istio Deployment be done |||

---

TestGatewayStandaloneInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Create Namespace | Namespace name: istio-system ||
| 2           | Create Secret     | Secret name: application-collection ||
| 3           | Install Istio AppCo with Gateway |||
| 4           | Wait for Istio Deployment be done |||

---

TestGatewayDiffNamespaceInstallation

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Create a new Namespace | Namespace name: random-name ||
| 2           | Create Secret     | Secret name: application-collection ||
| 3           | Install Istio AppCo in a different namespace |||
| 4           | Wait for Istio Deployment be done |||

---

TestInPlaceUpgrade

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Create Namespace | Namespace name: istio-system ||
| 2           | Create Secret     | Secret name: application-collection ||
| 3           | Install Istio AppCo in the Side Mode |||
| 4           | Wait for Istio Deployment be done |||
| 5           | Upgrate the Istio AppCo |||
| 6           | Wait for Istio Deployment be done |||

---

TestInstallWithCanaryUpgrade

| Step Number | Action               | Data         | Expected Result                |
| ----------- | -------------------- | ------------ | ------------------------------ |
| 1           | Create Namespace | Namespace name: istio-system ||
| 2           | Create Secret     | Secret name: application-collection ||
| 3           | Install Istio AppCo in the Side Mode |||
| 4           | Wait for Istio Deployment be done |||
| 5           | Upgrate the Istio AppCo using canary mode |||
| 6           | Wait for Istio Deployment be done |||

---
