# Rancher HA Install

Provisions a standalone K3s or RKE2 cluster on AWS EC2 and installs Rancher HA using the `ansible/rancher/default-ha/rancher-playbook.yml` playbook from [rancher-qa-infra-automation](https://github.com/rancherlabs/rancher-qa-infra-automation).

## Running the test

```bash
CATTLE_TEST_CONFIG=/path/to/config.yaml go test -v -run TestRancherHAInstallTestSuite \
  -tags validation \
  ./validation/pipeline/rancherha/install/...
```

## Config

All config lives under a single `qaInfraAutomation` key. Cluster type is auto-detected from the `kubernetesVersion` suffix (`+k3s` or `+rke2`).

```yaml
qaInfraAutomation:
  workspace: rancher-ha-test-0

  aws:
    accessKey: "your-access-key"
    secretKey: "your-secret-key"
    region: us-east-2
    ami: ami-012fd49f6b0c404c7
    sshUser: ubuntu
    awsSSHKeyName: jenkins-elliptic-validation.pem
    instanceType: t3a.xlarge
    vpc: vpc-bfccf4d7
    subnet: subnet-ee8cac86
    securityGroups:
      - sg-04f28c5d02555da26
    volumeSize: 20
    hostnamePrefix: myprefix
    route53Zone: qa.rancher.space

  standaloneCluster:
    kubernetesVersion: v1.35.3+k3s1
    kubeconfigOutputPath: kubeconfig.yaml
    serverFlags: "protect-kernel-defaults: true"
    # optionalFiles:
    #   - path: /etc/rancher/psa/custom-psa.yaml
        # url: https://example.com/psa-config.yaml
    nodes:
      - count: 3
        role:
          - etcd
          - cp
          - worker

  rancherInstall:
    chartVersion: "2.14.0"
    certManagerVersion: "v1.17.4"
    helmRepo: rancher-latest
    helmRepoURL: https://releases.rancher.com/server-charts/latest
    bootstrapPassword: admin
    password: "your-admin-password"
    cleanup: false
    # extraHelmValues:
    #   extraEnv[0].name: CATTLE_FEATURES
    #   extraEnv[0].value: "multi-cluster-management=true"

sshPath:
  sshPath: "/path/to/.ssh"
```

### TLS modes

Omit `tlsSource` for Rancher self-signed (default). For Let's Encrypt, set `tlsSource: letsEncrypt` and `letsEncryptEmail`. For a custom TLS secret, set `tlsSource: secret` with `tlsCertPath`, `tlsKeyPath`, and optionally `tlsCACertPath`.

### BYOK (Bring Your Own Kubeconfig)

Set `rancherInstall.existingKubeconfig` to skip cluster provisioning and install Rancher on an existing cluster.

## Jenkins parameters

The `rancher-ha-deploy` Jenkins job resolves credential placeholders (`${AWS_ACCESS_KEY_ID}`, `${AWS_SECRET_ACCESS_KEY}`, `${ADMIN_PASSWORD}`) in the CONFIG text, then injects individual parameters via `yq`:

| Jenkins Parameter | Config Path |
|---|---|
| `RANCHER_CLEANUP` | `.qaInfraAutomation.rancherInstall.cleanup` |
| `RANCHER_CHART_VERSION` | `.qaInfraAutomation.rancherInstall.chartVersion` |
| `RANCHER_IMAGE_TAG` | `.qaInfraAutomation.rancherInstall.imageTag` |
| `RANCHER_CERT_MANAGER_VERSION` | `.qaInfraAutomation.rancherInstall.certManagerVersion` |
| `RANCHER_LETSENCRYPT_EMAIL` | `.qaInfraAutomation.rancherInstall.letsEncryptEmail` |
| `RANCHER_HA_KUBECONFIG` | `.qaInfraAutomation.rancherInstall.existingKubeconfig` |
| `RANCHER_HA_CERT_OPTION` | `.qaInfraAutomation.rancherInstall.tlsSource` |
| `RANCHER_HELM_REPO` | `.qaInfraAutomation.rancherInstall.helmRepo` |
| `RANCHER_HELM_URL` | `.qaInfraAutomation.rancherInstall.helmRepoURL` |
| `RANCHER_HELM_EXTRA_SETTINGS` | `.qaInfraAutomation.rancherInstall.extraHelmValues.*` |
| `RANCHER_HOSTNAME_PREFIX` | `.qaInfraAutomation.aws.hostnamePrefix` |
| `AWS_AMI` | `.qaInfraAutomation.aws.ami` |
| `AWS_USER` | `.qaInfraAutomation.aws.sshUser` |
| `AWS_INSTANCE_TYPE` | `.qaInfraAutomation.aws.instanceType` |
| `AWS_REGION` | `.qaInfraAutomation.aws.region` |
| `AWS_VPC` | `.qaInfraAutomation.aws.vpc` |
| `AWS_SUBNET` | `.qaInfraAutomation.aws.subnet` |
| `AWS_SSH_KEY_NAME` | `.qaInfraAutomation.aws.awsSSHKeyName` |
| `AWS_ROUTE53_ZONE` | `.qaInfraAutomation.aws.route53Zone` |
| `AWS_VOLUME_SIZE` | `.qaInfraAutomation.aws.volumeSize` |
| `AWS_SECURITY_GROUPS` | `.qaInfraAutomation.aws.securityGroups[]` |
| `RANCHER_K3S_VERSION` / `RANCHER_RKE2_VERSION` | `.qaInfraAutomation.standaloneCluster.kubernetesVersion` |
| `RANCHER_K3S_NO_OF_SERVER_NODES` / `RANCHER_RKE2_NO_OF_SERVER_NODES` | `.qaInfraAutomation.standaloneCluster.nodes[0].count` |
| `RANCHER_K3S_SERVER_FLAGS` / `RANCHER_RKE2_SERVER_FLAGS` | `.qaInfraAutomation.standaloneCluster.serverFlags` |
| `RANCHER_OPTIONAL_FILES` | `.qaInfraAutomation.standaloneCluster.optionalFiles[]` |
