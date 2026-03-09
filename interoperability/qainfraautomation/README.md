# qainfraautomation

Go orchestration layer combining OpenTofu infrastructure provisioning with Ansible cluster configuration via the [rancher-qa-infra-automation](https://github.com/rancher/rancher-qa-infra-automation) repository.

## Configuration

All settings live under the `qaInfraAutomation` key in your cattle config YAML.

`ansible.configPath` is relative to `repoPath`. When set, `ANSIBLE_CONFIG` is exported to the playbook subprocess. When omitted, Ansible's normal search order applies — note there is no `ansible.cfg` at the repo root, so it will fall through to your global Ansible config.

**Exactly one** of `aws` or `harvester` must be set when using `customCluster`. If you set more than 1 it will cause issues.

`rancherCluster.nodeConfig` keys are provider-prefixed and passed verbatim to the tofu module. Supported providers: `aws`, `harvester`, `linode`. Cloud credentials are included directly in `nodeConfig` — no separate credential object is needed. Required credential keys per provider:

| Provider | Credential keys |
|---|---|
| `aws` | `aws_access_key`, `aws_secret_key`, `aws_region` |
| `linode` | `linode_token` |
| `harvester` | `harvester_cluster_v1_id`, `harvester_cluster_type`, `harvester_kubeconfig_content` |

```yaml
qaInfraAutomation:
  repoPath: /path/to/rancher-qa-infra-automation
  workspace: test-workspace

  ansible:
    configPath: ansible/rancher/downstream/custom_cluster/ansible.cfg

  aws:
    accessKey: <sensitive>
    secretKey: <sensitive>
    region: us-east-2
    ami: ami-xxxxxxxxxxxxxxxxx
    sshUser: ec2-user
    sshPublicKeyPath: /path/to/key.pub
    sshPrivateKeyPath: /path/to/key
    instanceType: t3a.xlarge
    vpc: vpc-xxxxxxxxxxxxxxxxx
    subnet: subnet-xxxxxxxxxxxxxxxxx
    securityGroups:
      - sg-xxxxxxxxxxxxxxxxx
    volumeSize: 50
    volumeType: gp3
    hostnamePrefix: prefix
    route53Zone: example.domain
    airgapSetup: false
    proxySetup: false
    nodes:
      - count: 1
        role: [etcd, cp]
      - count: 2
        role: [worker]

  harvester:
    kubeConfigPath: /path/to/harvester.yaml
    sshPublicKey: <sensitive>
    sshPrivateKeyPath: /path/to/key
    sshUser: ubuntu
    networkName: namespace/network-name      # VM NIC network (namespace/name format)
    imageId: namespace/image-name
    namespace: default
    generateName: prefix
    cpu: 4
    memory: 6Gi
    diskSize: 30Gi
    # Load balancer (only required when createLoadbalancer: true)
    createLoadbalancer: false
    backendNetworkName: network-name         # name only, no namespace prefix
    subnetCidr: 10.10.0.0/26
    gatewayIp: 10.10.0.1
    rangeIpStart: 10.10.0.10
    rangeIpEnd: 10.10.0.50
    ippoolName: ""                           # set to use an existing IP pool instead of creating one

  customCluster:
    kubernetesVersion: v1.34.4+k3s1
    generateName: prefix
    isNetworkPolicy: false
    psa: rancher-privileged
    harden: false
    nodes:
      - count: 1
        role: [etcd, cp]
      - count: 2
        role: [worker]

  standaloneCluster:
    kubernetesVersion: v1.34.4+k3s1
    cni: canal
    channel: stable
    kubeconfigOutputPath: /path/to/output/kubeconfig.yaml
    nodes:
      - count: 1
        role: [etcd, cp]
      - count: 2
        role: [worker]

  rancherCluster:
    kubernetesVersion: v1.34.4+rke2r1
    generateName: prefix
    isNetworkPolicy: false
    psa: rancher-privileged
    cloudProvider: harvester  # aws | harvester | linode
    machinePools:
      - controlPlaneRole: true
        etcdRole: true
        workerRole: false
        quantity: 1
      - controlPlaneRole: false
        etcdRole: false
        workerRole: true
        quantity: 2
    nodeConfig:
      # harvester
      harvester_cluster_v1_id: c-m-xxx
      harvester_cluster_type: imported
      harvester_kubeconfig_content: <sensitive>
      harvester_cpu_count: 4
      harvester_memory_size: 8Gi
      harvester_disk_size: 30Gi
      harvester_image_name: namespace/image-name
      harvester_network_name: namespace/network-name
      harvester_ssh_user: ubuntu
      # aws
      aws_access_key: <sensitive>
      aws_secret_key: <sensitive>
      aws_region: us-east-2
      aws_instance_type: t3a.xlarge
      aws_ami: ami-xxxxxxxxxxxxxxxxx
      aws_ssh_user: ec2-user
      # linode
      linode_token: <sensitive>
      linode_instance_type: g6-standard-4
      linode_region: us-west
      linode_ssh_user: root
```
