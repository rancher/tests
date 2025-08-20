#!/bin/bash
set -e

cat > cattle-config.yaml <<EOF
rancher:
  host: "${RANCHER_HOST}"
  adminToken: "${RANCHER_ADMIN_TOKEN}"
  cleanup: true
  insecure: true
  clusterName: "${CLUSTER_NAME}"
  adminPassword: "${RANCHER_ADMIN_PASSWORD}"

registryInput:
  name: "${QUAY_REGISTRY_NAME}"
  username: "${QUAY_REGISTRY_USERNAME}"
  password: "${QUAY_REGISTRY_PASSWORD}"

awsCredentials:
  accessKey: "${AWS_ACCESS_KEY_ID}"
  secretKey: "${AWS_SECRET_ACCESS_KEY}"
  defaultRegion: "${AWS_REGION}"

clusterConfig:
  machinePools:
  - machinePoolConfig:
      etcd: true
      controlplane: true
      worker: true
      quantity: 1
      drainBeforeDelete: true
      hostnameLengthLimit: 29
      nodeStartupTimeout: "600s"
      unhealthyNodeTimeout: "300s"
      maxUnhealthy: "2"
      unhealthyRange: "2-4"
  - machinePoolConfig:
      worker: true
      quantity: 2
  kubernetesVersion: "${KUBERNETES_VERSION}"
  provider: "${PROVIDER_AMAZON}"
  cni: "${CNI}"
  nodeProvider: "ec2"
  networking:
    stackPreference: "ipv4"
  hardened: false

awsMachineConfigs:
  region: "${AWS_REGION}"
  awsMachineConfig:
  - roles: ["etcd","controlplane","worker"]
    ami: "${AWS_AMI}"
    instanceType: "${AWS_INSTANCE_TYPE}"
    sshUser: "${AWS_USER}"
    vpcId: "${AWS_VPC_ID}"
    volumeType: "${AWS_VOLUME_TYPE}"
    zone: "${AWS_ZONE_LETTER}"
    retries: "5"
    rootSize: "${AWS_ROOT_SIZE}"
    securityGroup: [${AWS_QA_SECURITY_GROUP_NAMES}]

awsEC2Configs:
  region: "${AWS_REGION}"
  awsSecretAccessKey: "${AWS_SECRET_ACCESS_KEY}"
  awsAccessKeyID: "${AWS_ACCESS_KEY_ID}"
  awsEC2Config:
    - instanceType: "${AWS_INSTANCE_TYPE}"
      awsRegionAZ: "${AWS_REGION}${AWS_ZONE_LETTER}"
      awsAMI: "${AWS_AMI}"
      awsSecurityGroups: [${AWS_QA_SECURITY_GROUP_NAMES}]
      awsSSHKeyName: "${SSH_PRIVATE_KEY_NAME}.pem"
      awsCICDInstanceTag: "platform-qa"
      awsIAMProfile: "${AWS_IAM_PROFILE}"
      awsUser: "${AWS_USER}"
      volumeSize: "${AWS_ROOT_SIZE}"
      roles: ["etcd", "controlplane", "worker"]
sshPath: 
  sshPath: "${SSH_PRIVATE_KEY_PATH}"
EOF
