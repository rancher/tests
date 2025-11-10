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
    } catch (ignored) { }
    def buildDate = ''
    def vcsRef = ''
    try { buildDate = ctx.sh(script: "date -u +'%Y-%m-%dT%H:%M:%SZ'", returnStdout: true).trim() } catch (ignored) { }
    try { vcsRef = ctx.sh(script: 'git rev-parse --short HEAD 2>/dev/null || echo "unknown"', returnStdout: true).trim() } catch (ignored) { }
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
  def sshDir = './tests/.ssh'
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
  } catch (ignored) { }
  }

def extractArtifactsFromDockerVolume(ctx) {
  ctx.logInfo('Extracting artifacts from Docker shared volume to Jenkins workspace (library)')

  if (!ctx.env.VALIDATION_VOLUME) {
    ctx.error('Required environment variable VALIDATION_VOLUME is not set')
  }
  if (!ctx.env.BUILD_CONTAINER_NAME) {
    ctx.error('Required environment variable BUILD_CONTAINER_NAME is not set')
  }
  if (!ctx.env.TERRAFORM_VARS_FILENAME) {
    ctx.error('Required environment variable TERRAFORM_VARS_FILENAME is not set')
  }

  try {
    // Generate unique container name with UUID to prevent collisions
    def extractorContainerName = "${ctx.env.BUILD_CONTAINER_NAME}-extractor-${UUID.randomUUID().toString()}"
    def artifactsDir = 'artifacts'

    // Create artifacts directory with proper error handling
    ctx.sh "mkdir -p ${artifactsDir}"

    // Build shell script with proper escaping and validation
    def extractionScript = """
#!/bin/sh
set -e  # Exit on any error

# Create destination directories
mkdir -p /dest /dest/group_vars

# Function to safely copy file with logging
safe_copy() {
    local src="\$1"
    local dest="\$2"
    local description="\$3"

    if [ -f "\$src" ]; then
        if cp "\$src" "\$dest" 2>/dev/null; then
            echo "✓ Copied \$description: \$src -> \$dest"
        else
            echo "✗ Failed to copy \$description: \$src" >&2
            return 1
        fi
    else
        echo "- Skipping \$description (not found): \$src"
    fi
}

# Extract infrastructure outputs
safe_copy "/source/infrastructure-outputs.json" "/dest/" "infrastructure outputs" || \\
safe_copy "/source/shared/infrastructure-outputs.json" "/dest/" "shared infrastructure outputs"

# Extract Ansible inventory
safe_copy "/source/ansible/rke2/airgap/inventory.yml" "/dest/ansible-inventory.yml" "Ansible inventory" || \\
safe_copy "/source/ansible-inventory.yml" "/dest/" "Ansible inventory (root)"

# Extract Terraform variables file
safe_copy "/source/${ctx.env.TERRAFORM_VARS_FILENAME}" "/dest/" "Terraform variables file" || true

# Extract Terraform state files
safe_copy "/source/terraform.tfstate" "/dest/" "Terraform state" || \\
safe_copy "/source/shared/terraform.tfstate" "/dest/" "shared Terraform state"

safe_copy "/source/terraform-state-primary.tfstate" "/dest/" "primary Terraform state" || \\
safe_copy "/source/shared/terraform-state-primary.tfstate" "/dest/" "shared primary Terraform state"

# Extract backup state files
for f in /source/terraform-state-backup-*.tfstate /source/tfstate-backup-*.tfstate; do
    if [ -f "\$f" ]; then
        safe_copy "\$f" "/dest/" "backup state file" || true
    fi
done

# Extract kubeconfig with fallback search
if ! safe_copy "/source/kubeconfig.yaml" "/dest/" "kubeconfig"; then
    if ! safe_copy "/source/shared/kubeconfig.yaml" "/dest/" "shared kubeconfig"; then
        if ! safe_copy "/source/group_vars/kubeconfig.yaml" "/dest/" "group_vars kubeconfig"; then
            # Fallback recursive search for kubeconfig
            kc_path=\$(find /source -maxdepth 6 -type f -name "kubeconfig*" 2>/dev/null | head -1 || true)
            if [ -n "\$kc_path" ]; then
                safe_copy "\$kc_path" "/dest/kubeconfig.yaml" "found kubeconfig" || true
            else
                echo "- No kubeconfig found in any location"
            fi
        fi
    fi
fi

# Extract Ansible group variables
safe_copy "/source/group_vars/all.yml" "/dest/group_vars/" "Ansible group variables"

echo "Artifact extraction completed successfully"
"""

    // Execute Docker container with proper environment variable passing
    ctx.sh """
        docker run --rm \\
            -v ${ctx.env.VALIDATION_VOLUME}:/source \\
            -v \$(pwd)/${artifactsDir}:/dest:rw \\
            --name ${extractorContainerName} \\
            -e TERRAFORM_VARS_FILENAME="${ctx.env.TERRAFORM_VARS_FILENAME}" \\
            alpine:latest \\
            sh -c '${extractionScript}'
    """

    generateDeploymentSummary(ctx)
    ctx.logInfo('Artifact extraction completed successfully (library)')
  } catch (Exception e) {
    ctx.logError("Artifact extraction failed: ${e.message}")
    ctx.logError("Stack trace: ${e.getStackTrace()}")
    ctx.logWarning('Build will continue, but some artifacts may not be available for archival (library)')
  }
}

def generateDeploymentSummary(ctx) {
  ctx.logInfo('Generating deployment summary (library)')
  try {
    def artifactsDir = 'artifacts'
    ctx.sh "mkdir -p ${artifactsDir}"
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
      'artifacts/infrastructure-outputs.json',
      'artifacts/ansible-inventory.yml',
      "artifacts/${ctx.env.TERRAFORM_VARS_FILENAME}",
      'artifacts/terraform.tfstate'
    ]
    artifactFiles.each { fileName ->
      if (ctx.fileExists(fileName)) {
        summary.artifacts_generated.add(fileName)
      }
    }
    def summaryJson = groovy.json.JsonOutput.toJson(summary)
    ctx.writeFile file: 'artifacts/deployment-summary.json', text: groovy.json.JsonOutput.prettyPrint(summaryJson)
    ctx.logInfo('Deployment summary generated successfully (library)')
  } catch (Exception e) {
    ctx.logWarning("Failed to generate deployment summary: ${e.message}")
  }
}

def generateTofuConfiguration(ctx) {
  ctx.logInfo('Generating Terraform configuration (library)')
  if (!ctx.env.S3_BUCKET_NAME) { ctx.error('S3_BUCKET_NAME environment variable is not set') }
  if (!ctx.env.S3_BUCKET_REGION) { ctx.error('S3_BUCKET_REGION environment variable is not set') }
  if (!ctx.env.S3_KEY_PREFIX) { ctx.error('S3_KEY_PREFIX environment variable is not set') }
  ctx.sh 'mkdir -p qa-infra-automation/tofu/aws/modules/airgap'

  def wroteVars = false
  // Preferred: inline TERRAFORM_CONFIG from Jenkins parameter
  if (ctx.env.TERRAFORM_CONFIG) {
    def terraformConfig = ctx.env.TERRAFORM_CONFIG
    terraformConfig = terraformConfig.replace('${AWS_SECRET_ACCESS_KEY}', ctx.env.AWS_SECRET_ACCESS_KEY ?: '')
    terraformConfig = terraformConfig.replace('${AWS_ACCESS_KEY_ID}', ctx.env.AWS_ACCESS_KEY_ID ?: '')
    terraformConfig = terraformConfig.replace('${HOSTNAME_PREFIX}', ctx.env.HOSTNAME_PREFIX ?: '')
    ctx.dir('./qa-infra-automation/tofu/aws/modules/airgap') {
      ctx.writeFile file: ctx.env.TERRAFORM_VARS_FILENAME, text: terraformConfig
      ctx.logInfo("Terraform configuration written to: ${ctx.env.TERRAFORM_VARS_FILENAME}")
      wroteVars = true
    }
  } else {
    ctx.error('TERRAFORM_CONFIG environment variable is not set')
  }

  // Backend config (always write)
  ctx.dir('./qa-infra-automation/tofu/aws/modules/airgap') {
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

  if (!wroteVars) { ctx.error('Failed to prepare Terraform variables file') }
}

// ========================================
// Destruction pipeline helpers
// ========================================

def configureDestructionEnvironment(ctx) {
  ctx.logInfo('Configuring destruction environment (library)')
  generateDestructionEnvironmentFile(ctx)
  ensureDestructionSSHKeys(ctx)
  buildDockerImage(ctx)
  createSharedVolume(ctx)
  ctx.logInfo('Destruction environment configured successfully (library)')
}

def generateDestructionEnvironmentFile(ctx) {
  ctx.logInfo('Generating environment file for destruction containers (library)')

  def s3BucketName = ctx.env.S3_BUCKET_NAME ?: ctx.params.S3_BUCKET_NAME ?: 'jenkins-terraform-state-storage'
  def s3KeyPrefix = ctx.env.S3_KEY_PREFIX ?: ctx.params.S3_KEY_PREFIX ?: 'jenkins-airgap-rke2/terraform.tfstate'
  def s3BucketRegion = ctx.env.S3_BUCKET_REGION ?: ctx.params.S3_BUCKET_REGION ?: 'us-east-2'
  def awsRegion = ctx.env.AWS_REGION ?: ctx.params.S3_BUCKET_REGION ?: 'us-east-2'
  def workspace = ctx.env.TARGET_WORKSPACE ?: ctx.params.TARGET_WORKSPACE ?: ''

  if (workspace?.trim()) {
    if (!(s3KeyPrefix?.startsWith('env:/'))) {
      def parts = s3KeyPrefix.tokenize('/')
      def baseKey = parts ? parts[-1] : s3KeyPrefix
      s3KeyPrefix = "env:/${workspace}/${baseKey}"
      ctx.logInfo("Normalized S3_KEY_PREFIX to workspace-aware value: '${s3KeyPrefix}'")
    } else {
      ctx.logInfo("S3_KEY_PREFIX already workspace-scoped: '${s3KeyPrefix}'")
    }
  }

  ctx.logInfo("Using S3_BUCKET_NAME: '${s3BucketName}'")
  ctx.logInfo("Using S3_KEY_PREFIX: '${s3KeyPrefix}'")
  ctx.logInfo("Using S3_BUCKET_REGION: '${s3BucketRegion}'")
  ctx.logInfo("Using AWS_REGION: '${awsRegion}'")

  def envLines = [
    '# Environment variables for infrastructure destruction containers',
    '# NOTE: All sensitive credentials are passed via Jenkins withCredentials block for security',
    "TARGET_WORKSPACE=${ctx.env.TARGET_WORKSPACE}",
    "BUILD_NUMBER=${ctx.env.BUILD_NUMBER}",
    "JOB_NAME=${ctx.env.JOB_NAME}",
    "QA_INFRA_WORK_PATH=${ctx.env.QA_INFRA_WORK_PATH}",
    "TERRAFORM_VARS_FILENAME=${ctx.env.TERRAFORM_VARS_FILENAME}",
    "S3_BUCKET_NAME=${s3BucketName}",
    "S3_KEY_PREFIX=${s3KeyPrefix}",
    "S3_BUCKET_REGION=${s3BucketRegion}",
    "AWS_REGION=${awsRegion}",
    '',
    '# AWS Credentials excluded - will be passed via withCredentials',
    '',
    '# Terraform Variables for OpenTofu (TF_VAR_ prefix for automatic variable population)',
    'TF_VAR_aws_region=' + awsRegion
  ]

  ctx.writeFile file: ctx.env.ENV_FILE, text: envLines.join('\n')
  ctx.logInfo("Environment file created: ${ctx.env.ENV_FILE}")

  try {
    def preview = ctx.readFile(file: ctx.env.ENV_FILE).split('\n')
    def limit = preview.length < 10 ? preview.length : 10
    for (int i = 0; i < limit; i++) {
      ctx.logInfo(preview[i])
    }
  } catch (Exception ignored) {
    ctx.logWarning('Unable to read environment file for preview (library)')
  }
}

def ensureDestructionSSHKeys(ctx) {
  if (!ctx.env.AWS_SSH_PEM_KEY || !ctx.env.AWS_SSH_KEY_NAME) {
    ctx.logWarning('SSH key credentials not available; skipping SSH key setup (library)')
    return
  }

  ctx.logInfo('Setting up SSH keys for destruction workflow (library)')
  ctx.dir('./tests/.ssh') {
    ctx.sh 'mkdir -p . && chmod 700 .'
    def decodedKey = new String(ctx.env.AWS_SSH_PEM_KEY.decodeBase64())
    ctx.writeFile file: ctx.env.AWS_SSH_KEY_NAME, text: decodedKey
    ctx.sh "chmod 600 ${ctx.env.AWS_SSH_KEY_NAME}"
  }
  ctx.logInfo('SSH keys configured successfully (library)')
}

// Cleanup orchestration helpers
def shellQuote(value) {
  if (value == null) {
    return "''"
  }
  def text = value.toString()
  if (text.isEmpty()) {
    return "''"
  }
  return "'" + text.replace("'", "'\"'\"'") + "'"
}

def mergeCleanupOptions(ctx, Map options = [:]) {
  def merged = [:]
  if (options) {
    merged.putAll(options)
  }
  if (!merged.containsKey('workspace') && ctx.env.TF_WORKSPACE) {
    merged.workspace = ctx.env.TF_WORKSPACE
  }
  if (!merged.containsKey('varFile') && ctx.env.TERRAFORM_VARS_FILENAME) {
    merged.varFile = ctx.env.TERRAFORM_VARS_FILENAME
  }
  if (!merged.containsKey('cleanWorkspace')) {
    def envValue = ctx.env.CLEANUP_WORKSPACE
    merged.cleanWorkspace = envValue ? !envValue.equalsIgnoreCase('false') : true
  }
  if (!merged.containsKey('useLocalPath')) {
    def envValue = ctx.env.USE_REMOTE_PATH
    merged.useLocalPath = envValue ? envValue.equalsIgnoreCase('false') : false
  }
  if (!merged.containsKey('debug')) {
    def envValue = ctx.env.DEBUG
    merged.debug = envValue ? envValue.equalsIgnoreCase('true') : false
  }
  if (!merged.containsKey('destroyOnFailure')) {
    merged.destroyOnFailure = true
  }
  if (!merged.containsKey('verify')) {
    merged.verify = true
  }
  return merged
}

def buildCleanupArguments(Map options) {
  def args = []
  if (options.workspace) {
    args << "--workspace ${shellQuote(options.workspace)}"
  }
  if (options.varFile) {
    args << "--var-file ${shellQuote(options.varFile)}"
  }
  if (options.useLocalPath) {
    args << '--local-path'
  }
  if (options.containsKey('cleanWorkspace') && options.cleanWorkspace == false) {
    args << '--no-workspace-cleanup'
  }
  if (options.debug) {
    args << '--debug'
  }
  return args.join(' ')
}

def runCleanupWorkflow(ctx, Map options = [:]) {
  def commandArgs = buildCleanupArguments(options)
  def scriptLines = [
    '#!/bin/bash',
    'set -Eeuo pipefail',
    'cleanup_script="/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_infrastructure_cleanup.sh"',
    'if [ ! -f "${cleanup_script}" ]; then',
    '  echo "[ERROR] Cleanup script not found: ${cleanup_script}" >&2',
    '  exit 1',
    'fi'
  ]
  if (commandArgs?.trim()) {
    scriptLines << 'exec /bin/bash "$cleanup_script" ' + commandArgs.trim()
  } else {
    scriptLines << 'exec /bin/bash "$cleanup_script"'
  }
  def scriptContent = scriptLines.join('\n') + '\n'

  def extraEnv = [
    'TF_WORKSPACE'            : options.workspace ?: ctx.env.TF_WORKSPACE,
    'TERRAFORM_VARS_FILENAME' : options.varFile ?: ctx.env.TERRAFORM_VARS_FILENAME,
    'USE_REMOTE_PATH'         : (options.useLocalPath ? 'false' : 'true'),
    'CLEANUP_WORKSPACE'       : (options.cleanWorkspace == false ? 'false' : 'true'),
    'DEBUG'                   : (options.debug ? 'true' : 'false'),
    'DESTROY_ON_FAILURE'      : (options.destroyOnFailure ? 'true' : 'false')
  ].findAll { it.value != null }

  def helpers = ctx.ciHelpers()
  if (helpers) {
    helpers.executeScriptInContainer(ctx, scriptContent, extraEnv)
  } else if (ctx.metaClass.respondsTo(ctx, 'executeScriptInContainer')) {
    ctx.executeScriptInContainer(scriptContent, extraEnv)
  } else {
    ctx.error('No execution helper available for destruction script (library)')
  }
  ctx.logInfo('Infrastructure cleanup workflow executed (library)')
}

def verifyCleanupState(ctx, Map options = [:]) {
  def scriptContent = '''
#!/bin/bash
set -Eeuo pipefail
cd "${QA_INFRA_WORK_PATH}"
STATE_OUTPUT="/root/post-cleanup-state.txt"
if tofu -chdir=tofu/aws/modules/airgap state list > "${STATE_OUTPUT}" 2>&1; then
  remaining=$(wc -l < "${STATE_OUTPUT}" | tr -d ' ')
  echo "[INFO] Remaining OpenTofu resources after cleanup: ${remaining}"
  if [ "${remaining}" -gt 0 ]; then
    echo "[ERROR] Resources still present after cleanup:"
    cat "${STATE_OUTPUT}"
    exit 2
  fi
  rm -f "${STATE_OUTPUT}" || true
else
  echo "[WARNING] Unable to inspect OpenTofu state; skipping verification."
fi
'''

  def extraEnv = [
    'TF_WORKSPACE'            : options.workspace ?: ctx.env.TF_WORKSPACE,
    'TERRAFORM_VARS_FILENAME' : options.varFile ?: ctx.env.TERRAFORM_VARS_FILENAME,
    'DEBUG'                   : (options.debug ? 'true' : 'false')
  ].findAll { it.value != null }

  def helpers = ctx.ciHelpers()
  if (helpers) {
    helpers.executeScriptInContainer(ctx, scriptContent, extraEnv)
  } else if (ctx.metaClass.respondsTo(ctx, 'executeScriptInContainer')) {
    ctx.executeScriptInContainer(scriptContent, extraEnv)
  } else {
    ctx.error('No execution helper available for cleanup verification (library)')
  }
  ctx.logInfo('Post-cleanup state verification completed (library)')
  return true
}

def destroyInfrastructure(ctx, Map options = [:]) {
  def cleanupOptions = mergeCleanupOptions(ctx, options)
  ctx.logInfo('Executing infrastructure destruction workflow (library)')

  runCleanupWorkflow(ctx, cleanupOptions)

  if (cleanupOptions.verify) {
    verifyCleanupState(ctx, cleanupOptions)
  } else {
    ctx.logInfo('Skipping cleanup verification per configuration (library)')
  }

  return true
}

def archiveDestructionResults(ctx) {
  ctx.logInfo('Archiving destruction results (library)')
  try {
    ctx.sh """
      CONTAINER_ID=\$(docker ps -aqf "name=${ctx.env.BUILD_CONTAINER_NAME}")
      if [ -n "\${CONTAINER_ID}" ]; then
        docker cp \${CONTAINER_ID}:${ctx.env.QA_INFRA_WORK_PATH}/destruction-summary.json ./ || true
        echo "Destruction results archived successfully"
      else
        echo "No container found to archive results from"
      fi
    """
  } catch (Exception e) {
    ctx.logError("Failed to archive destruction results: ${e.message}")
  }
}

def archiveDestructionFailureArtifacts(ctx) {
  ctx.logInfo('Archiving destruction failure artifacts (library)')
  try {
    def commands = [
      "cd ${ctx.env.QA_INFRA_WORK_PATH}",
      'tofu -chdir=tofu/aws/modules/airgap workspace list > workspace-list.txt 2>&1 || echo "No workspace list available"',
      'tofu -chdir=tofu/aws/modules/airgap state list > remaining-resources.txt 2>&1 || echo "No state available"',
      "echo 'Destruction failure artifact collection completed'"
    ]

    def helpers = ctx.ciHelpers()
    if (helpers) {
      helpers.executeInContainer(ctx, commands)
    } else if (ctx.metaClass.respondsTo(ctx, 'executeInContainer')) {
      ctx.executeInContainer(commands)
    } else {
      ctx.logWarning('No container execution helper available; skipping detailed failure artifact collection (library)')
      return
    }

    ctx.sh """
      CONTAINER_ID=\$(docker ps -aqf "name=${ctx.env.BUILD_CONTAINER_NAME}")
      if [ -n "\${CONTAINER_ID}" ]; then
        docker cp \${CONTAINER_ID}:${ctx.env.QA_INFRA_WORK_PATH}/workspace-list.txt ./ || true
        docker cp \${CONTAINER_ID}:${ctx.env.QA_INFRA_WORK_PATH}/remaining-resources.txt ./ || true
      else
        echo "No container found to archive failure artifacts from"
      fi
    """

    ctx.archiveArtifacts artifacts: 'workspace-list.txt,remaining-resources.txt', allowEmptyArchive: true
  } catch (Exception e) {
    ctx.logError("Failed to archive failure artifacts: ${e.message}")
  }
}

def cleanupS3WorkspaceDirectory(ctx) {
  ctx.logInfo('Cleaning up S3 workspace directory after destruction (library)')

  def required = ['S3_BUCKET_NAME', 'S3_BUCKET_REGION', 'S3_KEY_PREFIX', 'TF_WORKSPACE']
  def missing = required.findAll { !(ctx.env."${it}"?.trim()) }
  if (!missing.isEmpty()) {
    ctx.logWarning("Skipping S3 cleanup due to missing variables: ${missing.join(', ')} (library)")
    return
  }

  ctx.withCredentials([
    ctx.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
    ctx.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY')
  ]) {
    ctx.sh """
      cat > s3_cleanup.sh <<'EOF'
      #!/bin/bash
      set -e

      if aws s3 ls "s3://${ctx.env.S3_BUCKET_NAME}/env:/${ctx.env.TF_WORKSPACE}/" --region "${ctx.env.S3_BUCKET_REGION}" 2>/dev/null; then
        echo "Workspace directory found: s3://${ctx.env.S3_BUCKET_NAME}/env:/${ctx.env.TF_WORKSPACE}/"
        aws s3 ls "s3://${ctx.env.S3_BUCKET_NAME}/env:/${ctx.env.TF_WORKSPACE}/" --recursive --region "${ctx.env.S3_BUCKET_REGION}" || echo 'No contents found'
        aws s3 rm "s3://${ctx.env.S3_BUCKET_NAME}/env:/${ctx.env.TF_WORKSPACE}/" --recursive --region "${ctx.env.S3_BUCKET_REGION}"
        if aws s3 ls "s3://${ctx.env.S3_BUCKET_NAME}/env:/${ctx.env.TF_WORKSPACE}/" --region "${ctx.env.S3_BUCKET_REGION}" 2>/dev/null; then
          echo 'ERROR: Failed to delete workspace directory'
          exit 1
        else
          echo "[OK] Successfully deleted workspace directory: s3://${ctx.env.S3_BUCKET_NAME}/env:/${ctx.env.TF_WORKSPACE}/"
        fi
      else
        echo '[INFO] Workspace directory does not exist in S3 - nothing to clean up'
      fi
      EOF

      docker run --rm \
        -e AWS_ACCESS_KEY_ID="${ctx.env.AWS_ACCESS_KEY_ID}" \
        -e AWS_SECRET_ACCESS_KEY="${ctx.env.AWS_SECRET_ACCESS_KEY}" \
        -e AWS_DEFAULT_REGION="${ctx.env.S3_BUCKET_REGION}" \
        -v \$(pwd)/s3_cleanup.sh:/tmp/s3_cleanup.sh \
        amazon/aws-cli:latest \
        sh /tmp/s3_cleanup.sh

      rm -f s3_cleanup.sh
    """
  }

  ctx.logInfo('S3 cleanup completed successfully (library)')
}

// Basic logging helpers (library)
def logInfo(ctx, String msg) { ctx.echo "[INFO] ${new Date().format('yyyy-MM-dd HH:mm:ss')} ${msg}" }
def logWarning(ctx, String msg) { ctx.echo "[WARNING] ${new Date().format('yyyy-MM-dd HH:mm:ss')} ${msg}" }
def logError(ctx, String msg) { ctx.echo "[ERROR] ${new Date().format('yyyy-MM-dd HH:mm:ss')} ${msg}" }

// Central artifact defs
@NonCPS
static def getArtifactDefinitions() {
  return [
    'infrastructure': [ 'artifacts/infrastructure-outputs.json', 'artifacts/ansible-inventory.yml', 'artifacts/*.tfvars', 'artifacts/*.tfvars.json', '!artifacts/terraform-state-primary.tfstate' ],
    'ansible_prep': [ 'group_vars.tar.gz', 'group_vars/all.yml', 'ansible-preparation-report.txt' ],
    'rke2_deployment': [ 'artifacts/kubeconfig.yaml', 'artifacts/rke2_deployment_report.txt', 'artifacts/rke2_deployment.log', 'artifacts/kubectl-setup-logs.txt' ],
    'rancher_deployment': [ 'artifacts/rancher-deployment-logs.txt', 'artifacts/rancher-validation-logs.txt', 'artifacts/deployment-summary.json' ],
    'failure_common': [ 'artifacts/*.log', 'artifacts/error-*.txt', 'artifacts/ansible-debug-info.txt' ],
    'failure_infrastructure': [ 'artifacts/tfplan-backup', 'artifacts/infrastructure-outputs.json' ],
    'failure_ansible': [ 'artifacts/ansible-inventory.yml', 'artifacts/ansible-error-logs.txt', 'artifacts/ssh-setup-error-logs.txt' ],
    'failure_rke2': [ 'artifacts/rke2-deployment-error-logs.txt', 'artifacts/kubectl-setup-error-logs.txt' ],
    'failure_rancher': [ 'artifacts/rancher-deployment-error-logs.txt', 'artifacts/rancher-validation-logs.txt', 'artifacts/rancher-debug-info.txt' ],
    'success_complete': [ 'artifacts/kubeconfig.yaml', 'artifacts/infrastructure-outputs.json', 'artifacts/ansible-inventory.yml', 'artifacts/deployment-summary.json' ]
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
  def cleanupReason = ['timeout', 'aborted'].contains(failureType) ? failureType : 'deployment_failure'
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
