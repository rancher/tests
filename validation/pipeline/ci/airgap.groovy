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
    // No container usage here; image may not exist yet
  }
}

def prepareInfra(ctx) {
  ctx.logInfo('Preparing infrastructure components (library)')
  ctx.withCredentials(ctx.getCredentialsList()) {
    // Use library-local implementations to reduce Jenkinsfile surface area
    buildDockerImage(ctx)
    createSharedVolume(ctx)
    ensureSSHKeysInContainer(ctx)
    ctx.validateParameters()
    // Validate sensitive data handling after image is available
    ctx.validateSensitiveDataHandling()
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
 
/**
 * Checkout repositories used by the pipeline
 */
def checkoutRepositories(ctx) {
  ctx.logInfo('Checking out source repositories (library)')
 
  // Checkout Rancher Tests Repository
  ctx.dir('./tests') {
    ctx.logInfo("Cloning rancher tests repository from ${ctx.env.RANCHER_TEST_REPO_URL}")
    ctx.checkout([
      $class: 'GitSCM',
      branches: [[name: "*/${ctx.params.RANCHER_TEST_REPO_BRANCH}"]],
      extensions: [
        [$class: 'CleanCheckout'],
        [$class: 'CloneOption', depth: 1, shallow: true]
      ],
      userRemoteConfigs: [[
        url: ctx.env.RANCHER_TEST_REPO_URL,
      ]]
    ])
  }
 
  // Checkout QA Infrastructure Repository
  ctx.dir('./qa-infra-automation') {
    ctx.logInfo("Cloning qa-infra-automation repository from ${ctx.env.QA_INFRA_REPO}")
    ctx.logInfo("Using branch: ${ctx.params.QA_INFRA_REPO_BRANCH}")
    ctx.checkout([
      $class: 'GitSCM',
      branches: [[name: "*/${ctx.params.QA_INFRA_REPO_BRANCH}"]],
      extensions: [
        [$class: 'CleanCheckout'],
        [$class: 'CloneOption', depth: 1, shallow: true]
      ],
      userRemoteConfigs: [[
        url: ctx.env.QA_INFRA_REPO,
      ]]
    ])
    // Verify which branch was actually checked out
    try {
      def actualBranch = ctx.sh(script: 'git rev-parse --abbrev-ref HEAD', returnStdout: true).trim()
      def latestCommit = ctx.sh(script: 'git log -1 --oneline', returnStdout: true).trim()
      ctx.logInfo("Checked out branch: ${actualBranch}")
      ctx.logInfo("Latest commit: ${latestCommit}")
    } catch (ignored) {
      ctx.logWarning('Unable to determine branch/commit in QA infra repo')
    }
  }
 
  ctx.logInfo('Repository checkout completed successfully (library)')
}

// Deploy infrastructure (extracted from Jenkinsfile 'Deploy Infrastructure' stage)
def deployInfrastructure(ctx) {
  ctx.logInfo('Deploying infrastructure (library)')
  try {
    ctx.validateRequiredVariables([
      'QA_INFRA_WORK_PATH', 'TF_WORKSPACE',
      'TERRAFORM_VARS_FILENAME', 'TERRAFORM_BACKEND_CONFIG_FILENAME'
    ])
 
    // Generate configuration files in repo (library-local)
    generateTofuConfiguration(ctx)
 
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
      'S3_BUCKET_REGION': ctx.env.S3_BUCKET_REGION,
      'S3_KEY_PREFIX': ctx.env.S3_KEY_PREFIX,
      'AWS_REGION': ctx.env.AWS_REGION,
      // Credentials and sensitive data are passed into the docker container
      // via the pipeline's credential handling within executeScriptInContainer
      'AWS_SSH_PEM_KEY': ctx.env.AWS_SSH_PEM_KEY,
      'AWS_SSH_KEY_NAME': ctx.env.AWS_SSH_KEY_NAME
    ]
 
    ctx.dockerHelper().executeScriptInContainer(infraScript, infraEnvVars)
    // Use library-local artifact extraction to centralize behavior
    extractArtifactsFromDockerVolume(ctx)
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

def buildDockerImage(ctx) {
  ctx.logInfo("Building Docker image: ${ctx.env.IMAGE_NAME}")
  ctx.dir('.') {
    // Best-effort configuration step (quiet)
    try {
      ctx.sh './tests/validation/configure.sh > /dev/null 2>&1 || true'
    } catch (ignored) {}
    def buildDate = ''
    def vcsRef = ''
    try { buildDate = ctx.sh(script: "date -u +'%Y-%m-%dT%H:%M:%SZ'", returnStdout: true).trim() } catch (ignored) {}
    try { vcsRef = ctx.sh(script: 'git rev-parse --short HEAD 2>/dev/null || echo "unknown"', returnStdout: true).trim() } catch (ignored) {}
    ctx.sh """
        docker build . \\
            -f ./tests/validation/Dockerfile.tofu.e2e \\
            -t ${ctx.env.IMAGE_NAME} \\
            --build-arg BUILD_DATE=${buildDate} \\
            --build-arg VCS_REF=${vcsRef} \\
            --label "pipeline.build.number=${ctx.env.BUILD_NUMBER}" \\
            --label "pipeline.job.name=${ctx.env.JOB_NAME}" \\
            --quiet
    """
  }
  ctx.logInfo('Docker image built successfully (library)')
}

def createSharedVolume(ctx) {
  ctx.logInfo("Creating shared volume: ${ctx.env.VALIDATION_VOLUME}")
  ctx.sh "docker volume create --name ${ctx.env.VALIDATION_VOLUME} || true"
}

def ensureSSHKeysInContainer(ctx) {
  ctx.logInfo('Ensuring SSH keys are available in container (library)')
  def sshKeyName = ctx.env.AWS_SSH_KEY_NAME
  if (!sshKeyName) {
    ctx.logWarning('AWS_SSH_KEY_NAME not set, cannot copy SSH keys')
    return
  }
  def sshDir = "./tests/.ssh"
  def keyPath = "${sshDir}/${sshKeyName}"
  if (!ctx.fileExists(keyPath)) {
    ctx.logWarning("SSH key not found at: ${keyPath}")
    // Try to recreate them
    ctx.withCredentials(ctx.getCredentialsList()) {
      try {
        // call pipeline-level setup (may create files under workspace)
        // Avoid using metaClass on the pipeline Binding (sandbox restrictions).
        // Instead, attempt the preferred hook and fall back, catching MissingMethodException.
        try {
          ctx.setupSSHKeysSecure()
        } catch (groovy.lang.MissingMethodException | java.lang.NoSuchMethodError ignored1) {
          try {
            ctx.setupSSHKeys()
          } catch (groovy.lang.MissingMethodException | java.lang.NoSuchMethodError ignored2) {
            ctx.logWarning('No setupSSHKeys hook available in pipeline context')
          }
        } catch (Exception e) {
          // If the hook exists but failed, surface the error up the usual path
          throw e
        }
        ctx.logInfo('SSH keys recreated (library)')
      } catch (Exception e) {
        ctx.logError("Failed to recreate SSH keys: ${e.message}")
        return
      }
    }
  }

  try {
    ctx.sh """
      docker run --rm \\
        -v ${ctx.env.VALIDATION_VOLUME}:/target \\
        -v \$(pwd)/${sshDir}:/source:ro \\
        alpine:latest \\
        sh -c '
          mkdir -p /target/.ssh
          chmod 700 /target/.ssh
          cp /source/* /target/.ssh/ || { echo "Failed to copy keys"; exit 1; }
          chmod 600 /target/.ssh/* || true
          echo "SSH keys copied successfully"
        '
    """
    ctx.logInfo('SSH keys successfully copied to container volume (library)')
  } catch (Exception e) {
    ctx.logError("Failed to copy SSH keys to container: ${e.message}")
    throw e
  }
}

def cleanupContainersAndVolumes(ctx) {
  ctx.logInfo('Cleaning up Docker containers and volumes (library)')
  try {
    ctx.sh """
        docker ps -aq --filter "name=${ctx.env.BUILD_CONTAINER_NAME}" | xargs -r docker stop || true
        docker ps -aq --filter "name=${ctx.env.BUILD_CONTAINER_NAME}" | xargs -r docker rm -v || true
        docker rmi -f ${ctx.env.IMAGE_NAME} || true
        docker volume rm -f ${ctx.env.VALIDATION_VOLUME} || true
        docker system prune -f || true
    """
  } catch (Exception e) {
    ctx.logWarning("Docker cleanup encountered issues: ${e.message}")
  }
  // attempt ssh cleanup if pipeline provides it
  try {
    ctx.cleanupSSHKeys()
  } catch (groovy.lang.MissingMethodException | java.lang.NoSuchMethodError ignored) {
    // noop - cleanup hook not provided by pipeline
  } catch (Exception ignored) {
    // ignore any cleanup errors to avoid masking primary failures
  }
  // shred env file if exists
  try {
    if (ctx.fileExists(ctx.env.ENV_FILE)) {
      ctx.sh "shred -vfz -n 3 ${ctx.env.ENV_FILE} 2>/dev/null || rm -f ${ctx.env.ENV_FILE}"
      ctx.logInfo('Environment file securely shredded (library)')
    }
  } catch (ignored) {}
}

def extractArtifactsFromDockerVolume(ctx) {
  ctx.logInfo('Extracting artifacts from Docker shared volume to Jenkins workspace (library)')
  try {
    def timestamp = System.currentTimeMillis()
    def extractorContainerName = "${ctx.env.BUILD_CONTAINER_NAME}-extractor-${timestamp}"
    ctx.sh """
        docker run --rm \\
            -v ${ctx.env.VALIDATION_VOLUME}:/source \\
            -v \$(pwd):/dest \\
            --name ${extractorContainerName} \\
            -e TERRAFORM_VARS_FILENAME=${ctx.env.TERRAFORM_VARS_FILENAME} \\
            alpine:latest \\
            sh -c '
                [ -f /source/infrastructure-outputs.json ] && cp /source/infrastructure-outputs.json /dest/ || true
                if [ -f /source/ansible/rke2/airgap/inventory.yml ]; then
                    cp /source/ansible/rke2/airgap/inventory.yml /dest/ansible-inventory.yml
                elif [ -f /source/ansible-inventory.yml ]; then
                    cp /source/ansible-inventory.yml /dest/
                fi
                [ -f "/source/${ctx.env.TERRAFORM_VARS_FILENAME}" ] && cp "/source/${ctx.env.TERRAFORM_VARS_FILENAME}" /dest/ || true
                [ -f /source/terraform.tfstate ] && cp /source/terraform.tfstate /dest/ || true
                [ -f /source/terraform-state-primary.tfstate ] && cp /source/terraform-state-primary.tfstate /dest/ || true
                for backup_file in /source/terraform-state-backup-*.tfstate /source/tfstate-backup-*.tfstate; do
                    [ -f "\\$backup_file" ] && cp "\\$backup_file" /dest/ || true
                done
                if [ -f /source/kubeconfig.yaml ]; then
                    cp /source/kubeconfig.yaml /dest/
                elif [ -f /source/group_vars/kubeconfig.yaml ]; then
                    cp /source/group_vars/kubeconfig.yaml /dest/
                fi
                if [ -f /source/group_vars/all.yml ]; then
                    mkdir -p /dest/group_vars
                    cp /source/group_vars/all.yml /dest/group_vars/all.yml
                fi
            '
    """
    generateDeploymentSummary(ctx)
    ctx.logInfo('Artifact extraction completed successfully (library)')
  } catch (Exception e) {
    ctx.logError("Artifact extraction failed: ${e.message}")
    ctx.logWarning('Build will continue, but some artifacts may not be available for archival (library)')
  }
}

def generateDeploymentSummary(ctx) {
  ctx.logInfo('Generating deployment summary (library)')
  try {
    def timestamp = new Date().format('yyyy-MM-dd HH:mm:ss')
    def summary = [
      deployment_info: [
        timestamp: timestamp,
        build_number: ctx.env.BUILD_NUMBER,
        job_name: ctx.env.JOB_NAME,
        workspace: ctx.env.TF_WORKSPACE,
        rke2_version: ctx.env.RKE2_VERSION,
        rancher_version: ctx.env.RANCHER_VERSION,
        rancher_hostname: ctx.env.RANCHER_HOSTNAME
      ],
      infrastructure: [
        terraform_vars_file: ctx.env.TERRAFORM_VARS_FILENAME,
        s3_bucket: ctx.env.S3_BUCKET_NAME,
        s3_bucket_region: ctx.env.S3_BUCKET_REGION,
        hostname_prefix: ctx.env.HOSTNAME_PREFIX
      ],
      artifacts_generated: []
    ]
    def artifactFiles = [
      'infrastructure-outputs.json',
      'ansible-inventory.yml',
      ctx.env.TERRAFORM_VARS_FILENAME,
      'terraform.tfstate'
    ]
    artifactFiles.each { fileName ->
      if (ctx.fileExists(fileName)) {
        summary.artifacts_generated.add(fileName)
      }
    }
    def summaryJson = groovy.json.JsonOutput.toJson(summary)
    ctx.writeFile file: 'deployment-summary.json', text: groovy.json.JsonOutput.prettyPrint(summaryJson)
    ctx.logInfo('Deployment summary generated successfully (library)')
  } catch (Exception e) {
    ctx.logWarning("Failed to generate deployment summary: ${e.message}")
  }
}

def generateTofuConfiguration(ctx) {
  ctx.logInfo('Generating Terraform configuration (library)')
  if (!ctx.env.TERRAFORM_CONFIG) { ctx.error('TERRAFORM_CONFIG environment variable is not set') }
  if (!ctx.env.S3_BUCKET_NAME) { ctx.error('S3_BUCKET_NAME environment variable is not set') }
  if (!ctx.env.S3_BUCKET_REGION) { ctx.error('S3_BUCKET_REGION environment variable is not set') }
  if (!ctx.env.S3_KEY_PREFIX) { ctx.error('S3_KEY_PREFIX environment variable is not set') }
  ctx.sh 'mkdir -p qa-infra-automation/tofu/aws/modules/airgap'
  def terraformConfig = ctx.env.TERRAFORM_CONFIG
  terraformConfig = terraformConfig.replace('${AWS_SECRET_ACCESS_KEY}', ctx.env.AWS_SECRET_ACCESS_KEY ?: '')
  terraformConfig = terraformConfig.replace('${AWS_ACCESS_KEY_ID}', ctx.env.AWS_ACCESS_KEY_ID ?: '')
  terraformConfig = terraformConfig.replace('${HOSTNAME_PREFIX}', ctx.env.HOSTNAME_PREFIX ?: '')
  ctx.dir('./qa-infra-automation') {
    ctx.dir('./tofu/aws/modules/airgap') {
      ctx.writeFile file: ctx.env.TERRAFORM_VARS_FILENAME, text: terraformConfig
      ctx.logInfo("Terraform configuration written to: ${ctx.env.TERRAFORM_VARS_FILENAME}")
      def backendConfig = """
terraform {
  backend "s3" {
    bucket = "${ctx.env.S3_BUCKET_NAME}"
    key    = "${ctx.env.S3_KEY_PREFIX}"
    region = "${ctx.env.S3_BUCKET_REGION}"
  }
}
"""
      ctx.writeFile file: ctx.env.TERRAFORM_BACKEND_CONFIG_FILENAME, text: backendConfig
      ctx.logInfo("S3 backend configuration written to: ${ctx.env.TERRAFORM_BACKEND_CONFIG_FILENAME}")
    }
  }
}

// Basic logging helpers (library)
def logInfo(ctx, String msg) { ctx.echo "[INFO] ${new Date().format('yyyy-MM-dd HH:mm:ss')} ${msg}" }
def logWarning(ctx, String msg) { ctx.echo "[WARNING] ${new Date().format('yyyy-MM-dd HH:mm:ss')} ${msg}" }
def logError(ctx, String msg) { ctx.echo "[ERROR] ${new Date().format('yyyy-MM-dd HH:mm:ss')} ${msg}" }

// Central artifact defs
@NonCPS
static def getArtifactDefinitions() {
  return [
    'infrastructure': [ 'infrastructure-outputs.json', 'ansible-inventory.yml', '*.tfvars' ],
    'ansible_prep': [ 'group_vars.tar.gz', 'group_vars/all.yml', 'ansible-preparation-report.txt' ],
    'rke2_deployment': [ 'kubeconfig.yaml', 'rke2_deployment_report.txt', 'rke2_deployment.log', 'kubectl-setup-logs.txt' ],
    'rancher_deployment': [ 'rancher-deployment-logs.txt', 'rancher-validation-logs.txt', 'deployment-summary.json' ],
    'failure_common': [ '*.log', 'error-*.txt', 'ansible-debug-info.txt' ],
    'failure_infrastructure': [ 'tfplan-backup', 'infrastructure-outputs.json' ],
    'failure_ansible': [ 'ansible-inventory.yml', 'ansible-error-logs.txt', 'ssh-setup-error-logs.txt' ],
    'failure_rke2': [ 'rke2-deployment-error-logs.txt', 'kubectl-setup-error-logs.txt' ],
    'failure_rancher': [ 'rancher-deployment-error-logs.txt', 'rancher-validation-logs.txt', 'rancher-debug-info.txt' ],
    'success_complete': [ 'kubeconfig.yaml', 'infrastructure-outputs.json', 'ansible-inventory.yml', 'deployment-summary.json' ]
  ]
}

def archiveArtifactsByType(ctx, String artifactType, List additionalFiles = []) {
  def defs = getArtifactDefinitions()
  if (!defs.containsKey(artifactType)) { artifactType = 'failure_common' }
  def artifactList = defs[artifactType] + additionalFiles
  try {
    ctx.archiveArtifacts(
      artifacts: artifactList.join(','),
      allowEmptyArchive: true,
      fingerprint: true,
      onlyIfSuccessful: false
    )
  } catch (Exception e) {
    logWarning(ctx, "Failed to archive ${artifactType} artifacts: ${e.message}")
  }
}

def archiveFailureArtifactsByType(ctx, String failureType) {
  def typeMap = [
    'deployment': 'failure_infrastructure',
    'ansible_prep': 'failure_ansible',
    'rke2': 'failure_rke2',
    'rancher': 'failure_rancher',
    'timeout': 'failure_infrastructure',
    'aborted': 'failure_common'
  ]
  def specific = typeMap[failureType] ?: 'failure_common'
  archiveArtifactsByType(ctx, specific)
  if (specific != 'failure_common') { archiveArtifactsByType(ctx, 'failure_common') }
}

// Archive a list of artifacts from pipeline context
def archiveBuildArtifacts(ctx, List artifactList = []) {
  try {
    ctx.archiveArtifacts(
      artifacts: artifactList.join(','),
      allowEmptyArchive: true
    )
  } catch (Exception e) {
    ctx.echo "[WARN] Failed to archive artifacts: ${e.message}"
  }
}

// Execute infrastructure cleanup using consolidated script
// Mirrors Jenkinsfile fallback behavior
def executeInfrastructureCleanup(ctx, String failureType) {
  def cleanupReason = ['timeout','aborted'].contains(failureType) ? failureType : 'deployment_failure'
  def cleanupScript = '''
#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_cleanup.sh
perform_cleanup "${CLEANUP_REASON}" "${TF_WORKSPACE}" "true"
'''
  def cleanupEnvVars = [
    'CLEANUP_REASON'                   : cleanupReason,
    'DESTROY_ON_FAILURE'               : ctx.env.DESTROY_ON_FAILURE,
    'QA_INFRA_WORK_PATH'               : ctx.env.QA_INFRA_WORK_PATH,
    'TF_WORKSPACE'                     : ctx.env.TF_WORKSPACE,
    'TERRAFORM_BACKEND_CONFIG_FILENAME': ctx.env.TERRAFORM_BACKEND_CONFIG_FILENAME,
    'TERRAFORM_VARS_FILENAME'          : ctx.env.TERRAFORM_VARS_FILENAME
  ]
  def helper = ctx.dockerHelper()
  helper.executeScriptInContainer(cleanupScript, cleanupEnvVars)
  ctx.echo("[INFO] Infrastructure cleanup completed for ${failureType}")
}

return this
