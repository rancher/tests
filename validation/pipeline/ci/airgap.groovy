// Airgap pipeline step library (Phase 2)

/**
 * Thin library functions called from Jenkinsfile stages.
 * Each function receives the pipeline script context as `ctx`
 * and performs the core action while delegating environment, credentials,
 * and cleanup behaviors back to the pipeline where appropriate.
 */

def configureEnv(ctx) {
  ctx.logInfo('Configuring deployment environment (library)')
  ctx.withCredentials(ctx.getCredentialsList()) {
    ctx.validateSensitiveDataHandling()
  }
}

def prepareInfra(ctx) {
  ctx.logInfo('Preparing infrastructure components (library)')
  ctx.withCredentials(ctx.getCredentialsList()) {
    ctx.buildDockerImage()
    ctx.createSharedVolume()
    ctx.ensureSSHKeysInContainer()
    ctx.validateParameters()
  }
}

def setupEnv(ctx) {
  ctx.deleteDir()
  ctx.configureEnvironmentComplete()
  try {
    def helpers = ctx.ciHelpers()
    if (helpers) {
      def cfg = helpers.readParamsYaml(ctx, 'config/params.yaml')
      if (cfg && cfg instanceof Map && !cfg.isEmpty()) {
        ctx.logInfo("Loaded config/params.yaml with ${cfg.keySet().size()} keys")
      }
    }
  } catch (ignored) {
    ctx.logWarning('config/params.yaml not found or unreadable')
  }
  ctx.logInfo("Build container: ${ctx.env.BUILD_CONTAINER_NAME}")
  ctx.logInfo("Docker image: ${ctx.env.IMAGE_NAME}")
  ctx.logInfo("Volume: ${ctx.env.VALIDATION_VOLUME}")
}

// Deploy infrastructure (extracted from Jenkinsfile 'Deploy Infrastructure' stage)
def deployInfrastructure(ctx) {
  ctx.logInfo('Deploying infrastructure (library)')
  try {
    ctx.validateRequiredVariables([
      'QA_INFRA_WORK_PATH', 'TF_WORKSPACE',
      'TERRAFORM_VARS_FILENAME', 'TERRAFORM_BACKEND_CONFIG_FILENAME'
    ])

    // Generate configuration files in repo
    ctx.generateTofuConfiguration()

    // Build inline script that the container will execute
    def infraScript = '''
#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_infrastructure_deploy.sh
'''
    def infraEnvVars = [
      'RKE2_VERSION': ctx.env.RKE2_VERSION,
      'RANCHER_VERSION': ctx.env.RANCHER_VERSION,
      'HOSTNAME_PREFIX': ctx.env.HOSTNAME_PREFIX,
      'RANCHER_HOSTNAME': ctx.env.RANCHER_HOSTNAME,
      'PRIVATE_REGISTRY_URL': ctx.env.PRIVATE_REGISTRY_URL,
      'PRIVATE_REGISTRY_USERNAME': ctx.env.PRIVATE_REGISTRY_USERNAME,
      'UPLOAD_CONFIG_TO_S3': 'true',
      'S3_BUCKET_NAME': ctx.env.S3_BUCKET_NAME,
      'S3_REGION': ctx.env.S3_REGION,
      'S3_KEY_PREFIX': ctx.env.S3_KEY_PREFIX,
      'AWS_REGION': ctx.env.AWS_REGION,
      'AWS_ACCESS_KEY_ID': ctx.env.AWS_ACCESS_KEY_ID,
      'AWS_SECRET_ACCESS_KEY': ctx.env.AWS_SECRET_ACCESS_KEY,
      'AWS_SSH_PEM_KEY': ctx.env.AWS_SSH_PEM_KEY,
      'AWS_SSH_KEY_NAME': ctx.env.AWS_SSH_KEY_NAME
    ]

    ctx.dockerHelper().executeScriptInContainer(infraScript, infraEnvVars)
    ctx.extractArtifactsFromDockerVolume()
    ctx.logInfo('Infrastructure deployed successfully (library)')
  } catch (Exception e) {
    ctx.logError("Infrastructure deployment failed (library): ${e.message}")
    // Delegate cleanup to the pipeline-scoped handler
    try {
      ctx.handleFailureCleanup('deployment')
    } catch (cleanupEx) {
      ctx.logWarning("Cleanup during deployInfrastructure failed: ${cleanupEx.message}")
    }
    throw e
  }
}

// Prepare Ansible environment (extracted from Jenkinsfile 'Prepare Ansible Environment' stage)
def prepareAnsibleEnv(ctx) {
  ctx.logInfo('Preparing Ansible environment (library)')
  ctx.validateRequiredVariables(['QA_INFRA_WORK_PATH', 'ANSIBLE_VARS_FILENAME'])

  def ansiblePrepScript = '''
#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/ansible_prepare_environment.sh
'''
  def ansiblePrepEnvVars = [
    'ANSIBLE_VARIABLES': ctx.env.ANSIBLE_VARIABLES,
    'RKE2_VERSION': ctx.env.RKE2_VERSION,
    'RANCHER_VERSION': ctx.env.RANCHER_VERSION,
    'HOSTNAME_PREFIX': ctx.env.HOSTNAME_PREFIX,
    'RANCHER_HOSTNAME': ctx.env.RANCHER_HOSTNAME,
    'PRIVATE_REGISTRY_URL': ctx.env.PRIVATE_REGISTRY_URL,
    'PRIVATE_REGISTRY_USERNAME': ctx.env.PRIVATE_REGISTRY_USERNAME,
    'PRIVATE_REGISTRY_PASSWORD': ctx.env.PRIVATE_REGISTRY_PASSWORD,
    'SKIP_YAML_VALIDATION': ctx.env.SKIP_YAML_VALIDATION,
    'AWS_SSH_PEM_KEY': ctx.env.AWS_SSH_PEM_KEY,
    'AWS_SSH_KEY_NAME': ctx.env.AWS_SSH_KEY_NAME
  ]

  try {
    ctx.dockerHelper().executeScriptInContainer(ansiblePrepScript, ansiblePrepEnvVars)
    ctx.logInfo('Ansible environment prepared (library)')
  } catch (Exception e) {
    ctx.logError("Ansible preparation failed (library): ${e.message}")
    ctx.handleFailureCleanup('ansible_prep')
    throw e
  }
}

// Deploy RKE2 via Ansible (extracted from Jenkinsfile 'Deploy RKE2 with Ansible' stage)
def deployRKE2(ctx) {
  ctx.logInfo('Deploying RKE2 cluster (library)')
  ctx.validateRequiredVariables(['QA_INFRA_WORK_PATH', 'ANSIBLE_VARS_FILENAME'])

  def rke2Script = '''
#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/ansible_deploy_rke2.sh
'''
  def rke2EnvVars = [
    'RKE2_VERSION': ctx.env.RKE2_VERSION,
    'SKIP_VALIDATION': 'false',
    'AWS_SSH_PEM_KEY': ctx.env.AWS_SSH_PEM_KEY,
    'AWS_SSH_KEY_NAME': ctx.env.AWS_SSH_KEY_NAME
  ]

  try {
    ctx.dockerHelper().executeScriptInContainer(rke2Script, rke2EnvVars)
    ctx.logInfo('RKE2 deployment completed (library)')
  } catch (Exception e) {
    ctx.logError("RKE2 deployment failed (library): ${e.message}")
    ctx.handleFailureCleanup('rke2')
    throw e
  }
}

// Deploy Rancher via Ansible (extracted from Jenkinsfile 'Deploy Rancher with Ansible' stage)
def deployRancher(ctx) {
  ctx.logInfo('Deploying Rancher (library)')
  ctx.validateRequiredVariables(['QA_INFRA_WORK_PATH', 'ANSIBLE_VARS_FILENAME'])

  def rancherScript = '''
#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/ansible_deploy_rancher.sh
'''
  def rancherEnvVars = [
    'RANCHER_VERSION': ctx.env.RANCHER_VERSION,
    'HOSTNAME_PREFIX': ctx.env.HOSTNAME_PREFIX,
    'RANCHER_HOSTNAME': ctx.env.RANCHER_HOSTNAME,
    'SKIP_VERIFICATION': 'false'
  ]

  try {
    ctx.dockerHelper().executeScriptInContainer(rancherScript, rancherEnvVars)
    ctx.logInfo('Rancher deployment completed (library)')
  } catch (Exception e) {
    ctx.logError("Rancher deployment failed (library): ${e.message}")
    ctx.handleFailureCleanup('rancher')
    throw e
  }
}

// Lightweight wrapper to archive common artifacts from pipeline scope
def archiveCommonArtifacts(ctx, artifactList = []) {
  try {
    ctx.archiveBuildArtifacts(artifactList)
  } catch (Exception e) {
    ctx.logWarning("archiveCommonArtifacts failed: ${e.message}")
  }
}

return this
