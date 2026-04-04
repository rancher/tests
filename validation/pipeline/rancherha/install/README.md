# Rancher HA Install

Provisions a standalone K3s or RKE2 cluster on AWS EC2 and installs Rancher HA on it using the `ansible/rancher/default-ha/rancher-playbook.yml` playbook from [rancher-qa-infra-automation](https://github.com/rancherlabs/rancher-qa-infra-automation).

## Running the test

```bash
CATTLE_CONFIG=/path/to/config.yaml go test -v -run TestRancherHAInstallTestSuite \
  -tags validation \
  ./validation/pipeline/rancherha/install/...
```

## Config

All config lives under a single `qaInfraAutomation` key.

```yaml
qaInfraAutomation:
  workspace: rancher-ha-test-0

  aws:
    accessKey: <AWS_ACCESS_KEY>
    secretKey: <AWS_SECRET_KEY>
    region: us-west-1
    ami: ami-0e01311d1f112d4d0
    sshUser: ec2-user
    awsSSHKeyName: jenkins-elliptic-validation
    instanceType: t3a.xlarge
    vpc: vpc-c30925a4
    subnet: subnet-6d011e0a
    securityGroups:
      - sg-0a122b0d588c87673
    volumeSize: 50       # optional, default: 50
    volumeType: gp3      # optional, default: gp3
    hostnamePrefix: ctw-t
    route53Zone: qa.rancher.space

  standaloneCluster:
    # Use +k3s suffix for K3s, +rke2 suffix for RKE2 — cluster type is auto-detected.
    kubernetesVersion: v1.33.5+k3s1
    cni: calico
    # channel: stable   # optional: K3s/RKE2 release channel
    kubeconfigOutputPath: ansible/k3s/default/kubeconfig.yaml  # use ansible/rke2/default/kubeconfig.yaml for RKE2
    # serverFlags: "--disable traefik"   # optional: extra K3s/RKE2 server flags
    # optionalFiles:                     # optional: files to download before provisioning
    #   - path: /absolute/dest/path
    #     url: https://example.com/file.yaml
    nodes:
      - count: 1
        role:
          - etcd
          - cp
          - worker

  rancherInstall:
    # existingKubeconfig: /path/to/kubeconfig.yaml  # optional: skip provisioning and use an existing cluster

    chartVersion: ">=0.0.0-0"
    imageTag: "latest"
    helmRepo: rancher-alpha
    helmRepoURL: https://releases.rancher.com/server-charts/alpha
    bootstrapPassword: admin
    password: "set this to something custom"
    cleanup: false   # optional, default: true — set false to keep EC2 nodes alive after the test

    # TLS — choose one of the three modes below; omit tlsSource for Rancher self-signed (default).
    #
    # Mode 1: Rancher self-signed (default — no extra fields needed)
    # certManagerVersion: "1.17.4"   # required for self-signed; omit to skip cert-manager install
    #
    # Mode 2: Let's Encrypt
    # certManagerVersion: "1.17.4"   # required for letsEncrypt
    # tlsSource: letsEncrypt
    # letsEncryptEmail: ops@example.com
    #
    # Mode 3: Custom TLS secret (playbook creates tls-rancher-ingress in cattle-system)
    # certManagerVersion: ""         # cert-manager not needed for custom secret
    # tlsSource: secret
    # tlsCertPath: /path/to/tls.crt
    # tlsKeyPath: /path/to/tls.key
    # tlsCACertPath: /path/to/ca.pem  # optional — omit if cert is publicly trusted; enables privateCA when set
```
