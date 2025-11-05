#!/usr/bin/env groovy

/**
 * Shared Library for Airgap RKE2 Jenkins Pipelines
 *
 * This library provides common functions shared between setup and destroy pipelines,
 * eliminating duplication and ensuring consistency across jobs.
 *
 * Usage:
 *   def common = load('validation/pipeline/common/airgap_common.groovy')
 *   common.init(this)
 *   common.logInfo('Message')
 */

// ========================================
// CONSTANTS AND CONFIGURATION
// ========================================

/**
 * Centralized pipeline configuration constants
 * Shared across setup and destroy pipelines
 */
class CommonConfig {
    static final String DEFAULT_S3_BUCKET = 'rancher-terraform-state'
    static final String DEFAULT_S3_BUCKET_REGION = 'us-east-1'
    static final String DOCKER_BUILD_CONTEXT = '.'
    static final String DOCKERFILE_PATH = 'tests/validation/Dockerfile.tofu.e2e'
    static final String LOG_PREFIX_INFO = '[INFO]'
    static final String LOG_PREFIX_ERROR = '[ERROR]'
    static final String LOG_PREFIX_WARNING = '[WARNING]'
}

// Reference to pipeline context
def context

/**
 * Initialize library with pipeline context
 * @param pipelineContext The pipeline 'this' reference
 */
def init(pipelineContext) {
    this.context = pipelineContext
    return this
}

// ========================================
// UTILITY FUNCTIONS
// ========================================

/**
 * Get environment variable or parameter with fallback to default
 * Tries env first, then params, then returns default
 *
 * @param name Variable name
 * @param defaultValue Default value if not found
 * @return Value from env, params, or default
 */
def getEnvOrParam(name, defaultValue = null) {
    // Try environment variable first
    def envValue = context.env."${name}"
    if (envValue && envValue != "null" && envValue.trim()) {
        return envValue.trim()
    }

    // Try parameter
    try {
        def paramValue = context.params."${name}"
        if (paramValue && paramValue != "null" && paramValue.trim()) {
            return paramValue.trim()
        }
    } catch (ignored) {
        // params may not be available in all contexts
    }

    return defaultValue
}

/**
 * Get S3 configuration from environment/parameters
 * @return Map with bucket, keyPrefix, and region
 */
def getS3Config() {
    return [
            bucket: getEnvOrParam('S3_BUCKET_NAME', CommonConfig.DEFAULT_S3_BUCKET),
            keyPrefix: getEnvOrParam('S3_KEY_PREFIX', 'jenkins-airgap-rke2/terraform.tfstate'),
            region: getEnvOrParam('S3_BUCKET_REGION', CommonConfig.DEFAULT_S3_BUCKET_REGION)
    ]
}

// ========================================
// LAZY LOADING HELPER
// ========================================

/**
 * Create a lazy loader for Groovy scripts
 * Returns a closure that loads the script on first access and caches it
 *
 * @param candidatePaths List of possible script paths to try
 * @return Closure that returns the loaded script
 */
def createLazyLoader(List<String> candidatePaths) {
    def scriptRef = null
    return {
        if (scriptRef == null) {
            for (p in candidatePaths) {
                try {
                    if (context.fileExists(p)) {
                        scriptRef = context.load(p)
                        logDebug("Loaded helper from: ${p}")
                        break
                    }
                } catch (ignored) {
                    // fileExists may not be available in some contexts
                }
            }
            if (scriptRef == null) {
                logWarning("Could not load helper from any of: ${candidatePaths}")
            }
        }
        return scriptRef
    }
}

// ========================================
// VALIDATION FUNCTIONS
// ========================================

/**
 * Validate that required environment variables are set
 * Fails the build if any required variables are missing
 *
 * @param requiredVars List of required variable names
 */
def validateRequiredVariables(List<String> requiredVars) {
    logInfo('Validating required environment variables')

    def missingVars = []
    requiredVars.each { varName ->
        def varValue = context.env."${varName}"
        if (!varValue || varValue.trim().isEmpty()) {
            missingVars.add(varName)
        }
    }

    if (!missingVars.isEmpty()) {
        def errorMsg = "Missing required environment variables: ${missingVars.join(', ')}"
        logError(errorMsg)
        context.error(errorMsg)
    }

    logInfo('All required variables validated successfully')
}

/**
 * Validate required parameters
 * Fails the build if any required parameters are missing
 *
 * @param requiredParams Map of parameter names to validation rules
 */
def validateParameters(Map requiredParams = [:]) {
    logInfo('Validating pipeline parameters')

    def errors = []

    requiredParams.each { paramName, validationRule ->
        def paramValue = context.params."${paramName}"

        if (validationRule == 'required' && (!paramValue || paramValue.trim().isEmpty())) {
            errors.add("${paramName} is required but was not provided")
        }
    }

    if (!errors.isEmpty()) {
        def errorMsg = "Parameter validation failed:\n- ${errors.join('\n- ')}"
        logError(errorMsg)
        context.error(errorMsg)
    }

    logInfo('All parameters validated successfully')
}

// ========================================
// CREDENTIALS MANAGEMENT
// ========================================

/**
 * Get standard credentials list for TFP jobs
 * Allows customization of which credentials to include
 *
 * @param options Map with optional flags: includeSSH, includeRegistry, includeQase
 * @return List of credential bindings
 */
def getCredentialsList(Map options = [:]) {
    def defaults = [
            includeSSH: true,
            includeRegistry: false,
            includeQase: false,
            includeAdmin: false
    ]
    def config = defaults + options

    def creds = [
            context.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
            context.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY'),
    ]

    if (config.includeSSH) {
        creds += [
                context.string(credentialsId: 'AWS_SSH_PEM_KEY', variable: 'AWS_SSH_PEM_KEY'),
                context.string(credentialsId: 'AWS_SSH_KEY_NAME', variable: 'AWS_SSH_KEY_NAME'),
        ]
    }

    if (config.includeRegistry) {
        creds += [
                context.string(credentialsId: 'RANCHER_REGISTRY_USER_NAME', variable: 'RANCHER_REGISTRY_USER_NAME'),
                context.string(credentialsId: 'RANCHER_REGISTRY_PASSWORD', variable: 'RANCHER_REGISTRY_PASSWORD'),
        ]
    }

    if (config.includeQase) {
        creds += [
                context.string(credentialsId: 'QASE_AUTOMATION_TOKEN', variable: 'QASE_AUTOMATION_TOKEN'),
        ]
    }

    if (config.includeAdmin) {
        creds += [
                context.string(credentialsId: 'ADMIN_PASSWORD', variable: 'ADMIN_PASSWORD'),
        ]
    }

    return creds
}

/**
 * Setup SSH keys with secure handling
 * Creates SSH directory and writes keys with proper permissions
 *
 * @param sshDir Directory to write SSH keys (default: './tests/.ssh')
 * @param comprehensive Whether to include public key generation and validation
 */
def setupSSHKeys(String sshDir = './tests/.ssh', boolean comprehensive = false) {
    if (!context.env.AWS_SSH_PEM_KEY || !context.env.AWS_SSH_KEY_NAME) {
        logWarning('SSH key configuration skipped - credentials not available')
        return
    }

    logInfo('Setting up SSH keys')

    try {
        context.dir(sshDir) {
            context.sh 'mkdir -p . && chmod 700 .'

            def decodedKey = new String(context.env.AWS_SSH_PEM_KEY.decodeBase64())
            context.writeFile file: context.env.AWS_SSH_KEY_NAME, text: decodedKey

            context.sh "chmod 600 ${context.env.AWS_SSH_KEY_NAME}"
            context.sh "chown \$(whoami):\$(whoami) ${context.env.AWS_SSH_KEY_NAME} 2>/dev/null || true"

            if (comprehensive) {
                // Validate key format
                def keyContent = context.sh(
                        script: "head -1 ${context.env.AWS_SSH_KEY_NAME}",
                        returnStdout: true
                ).trim()
                if (!keyContent.startsWith('-----BEGIN')) {
                    logWarning('SSH key format validation warning - unexpected format')
                }

                // Generate public key for SSH operations
                context.sh """
                    if [ -f "${context.env.AWS_SSH_KEY_NAME}" ]; then
                        ssh-keygen -f "${context.env.AWS_SSH_KEY_NAME}" -y > "${context.env.AWS_SSH_KEY_NAME}.pub" || echo "Failed to generate public key"
                        chmod 644 "${context.env.AWS_SSH_KEY_NAME}.pub" 2>/dev/null || true
                    fi
                """
            }
        }

        context.env.SSH_KEY_PATH = "${sshDir}/${context.env.AWS_SSH_KEY_NAME}"
        logInfo('SSH keys configured successfully')

    } catch (Exception e) {
        logError("SSH key setup failed: ${e.message}")
        cleanupSSHKeys(sshDir)
        throw e
    }
}

/**
 * Clean up SSH keys securely
 * Uses shred if available, falls back to rm
 *
 * @param sshDir SSH directory containing keys
 */
def cleanupSSHKeys(String sshDir = './tests/.ssh') {
    logInfo('Cleaning up SSH keys securely')

    try {
        context.withCredentials([
                context.string(credentialsId: 'AWS_SSH_KEY_NAME', variable: 'AWS_SSH_KEY_NAME')
        ]) {
            def awsSshKeyName = context.env.AWS_SSH_KEY_NAME

            if (awsSshKeyName) {
                def keyPath = "${sshDir}/${awsSshKeyName}"

                if (context.fileExists(keyPath)) {
                    try {
                        context.sh "shred -vfz -n 3 ${keyPath} 2>/dev/null || rm -f ${keyPath}"
                        logInfo("SSH key securely shredded: ${keyPath}")
                    } catch (Exception ignored) {
                        context.sh "rm -f ${keyPath}"
                        logWarning("SSH key deleted (shred unavailable): ${keyPath}")
                    }
                }

                // Clean up any temporary SSH files
                context.sh "rm -f ${sshDir}/known_hosts ${sshDir}/config 2>/dev/null || true"

                // Ensure SSH directory is secure
                if (context.fileExists(sshDir)) {
                    context.sh "chmod 700 ${sshDir} 2>/dev/null || true"
                }
            }
        }
    } catch (Exception e) {
        logWarning("SSH key cleanup encountered issues: ${e.message}")
    }

    logInfo('SSH key cleanup completed')
}

// ========================================
// ARTIFACT MANAGEMENT
// ========================================

/**
 * Archive build artifacts with error handling
 *
 * @param artifacts List of artifact file patterns or single comma-separated string
 */
def archiveBuildArtifacts(artifacts) {
    try {
        def artifactString = artifacts instanceof List ? artifacts.join(',') : artifacts
        context.archiveArtifacts artifacts: artifactString, allowEmptyArchive: true
        logInfo("Artifacts archived: ${artifactString}")
    } catch (Exception e) {
        logError("Failed to archive artifacts: ${e.message}")
    }
}

/**
 * Centralized artifact configuration map
 * Defines all artifact types and their associated files
 */
@groovy.transform.CompileStatic
static def getArtifactDefinitions() {
    return [
            'infrastructure': [
                    'infrastructure-outputs.json',
                    'ansible-inventory.yml',
                    '*.tfvars'
            ],
            'ansible_prep': [
                    'group_vars.tar.gz',
                    'group_vars/all.yml',
                    'ansible-preparation-report.txt'
            ],
            'rke2_deployment': [
                    'kubeconfig.yaml',
                    'rke2_deployment_report.txt',
                    'rke2_deployment.log',
                    'kubectl-setup-logs.txt'
            ],
            'rancher_deployment': [
                    'rancher-deployment-logs.txt',
                    'rancher-validation-logs.txt',
                    'deployment-summary.json'
            ],
            'failure_common': [
                    '*.log',
                    'error-*.txt',
                    'ansible-debug-info.txt'
            ],
            'failure_infrastructure': [
                    'tfplan-backup',
                    'infrastructure-outputs.json'
            ],
            'failure_ansible': [
                    'ansible-inventory.yml',
                    'ansible-error-logs.txt',
                    'ssh-setup-error-logs.txt'
            ],
            'failure_rke2': [
                    'rke2-deployment-error-logs.txt',
                    'kubectl-setup-error-logs.txt'
            ],
            'failure_rancher': [
                    'rancher-deployment-error-logs.txt',
                    'rancher-validation-logs.txt',
                    'rancher-debug-info.txt'
            ],
            'success_complete': [
                    'kubeconfig.yaml',
                    'infrastructure-outputs.json',
                    'ansible-inventory.yml',
                    'deployment-summary.json'
            ],
            'destruction': [
                    'destruction-plan.txt',
                    'destruction-summary.json',
                    'destruction-logs.txt'
            ],
            'destruction_failure': [
                    'workspace-list.txt',
                    'remaining-resources.txt',
                    'destruction-error-logs.txt'
            ]
    ]
}

/**
 * Archive artifacts based on type
 *
 * @param artifactType Type of artifacts to archive
 * @param additionalFiles Optional additional files to include
 */
def archiveArtifactsByType(String artifactType, List additionalFiles = []) {
    def artifactDefs = getArtifactDefinitions()

    if (!artifactDefs.containsKey(artifactType)) {
        logWarning("Unknown artifact type: ${artifactType}, using failure_common")
        artifactType = 'failure_common'
    }

    def artifactList = artifactDefs[artifactType] + additionalFiles

    logInfo("Archiving ${artifactType} artifacts")
    try {
        context.archiveArtifacts(
                artifacts: artifactList.join(','),
                allowEmptyArchive: true,
                fingerprint: true,
                onlyIfSuccessful: false
        )
    } catch (Exception e) {
        logError("Failed to archive ${artifactType} artifacts: ${e.message}")
    }
}

/**
 * Archive failure artifacts - combines common + specific
 *
 * @param failureType Type of failure (deployment, ansible_prep, rke2, rancher, timeout, aborted)
 */
def archiveFailureArtifactsByType(String failureType) {
    def typeMap = [
            'deployment': 'failure_infrastructure',
            'ansible_prep': 'failure_ansible',
            'rke2': 'failure_rke2',
            'rancher': 'failure_rancher',
            'timeout': 'failure_infrastructure',
            'aborted': 'failure_common',
            'destruction': 'destruction_failure'
    ]

    def specificType = typeMap[failureType] ?: 'failure_common'
    archiveArtifactsByType(specificType)

    if (specificType != 'failure_common') {
        archiveArtifactsByType('failure_common')
    }
}

// ========================================
// DOCKER OPERATIONS
// ========================================

/**
 * Create credential environment file for Docker containers
 * Securely writes credentials to temporary file
 *
 * @return Filename of created credential file
 */
def createCredentialEnvironmentFile() {
    def timestamp = System.currentTimeMillis()
    def credentialEnvFile = "docker-credentials-${timestamp}.env"

    def envContent = []

    if (context.env.AWS_ACCESS_KEY_ID) {
        envContent.add("AWS_ACCESS_KEY_ID=${context.env.AWS_ACCESS_KEY_ID}")
    }
    if (context.env.AWS_SECRET_ACCESS_KEY) {
        envContent.add("AWS_SECRET_ACCESS_KEY=${context.env.AWS_SECRET_ACCESS_KEY}")
    }

    context.writeFile file: credentialEnvFile, text: envContent.join('\n')
    context.sh "chmod 600 ${credentialEnvFile}"

    logDebug("Created credential environment file: ${credentialEnvFile}")
    return credentialEnvFile
}

/**
 * Add credential environment file to Docker command
 * Inserts --env-file flag into Docker command string
 *
 * @param dockerCmd Original Docker command
 * @param credentialEnvFile Credential file to add
 * @return Modified Docker command with --env-file
 */
def addCredentialEnvFileToDockerCommand(String dockerCmd, String credentialEnvFile) {
    if (!credentialEnvFile) {
        return dockerCmd
    }

    def modifiedCmd = dockerCmd
    def insertionPoint = modifiedCmd.lastIndexOf('--name')

    if (insertionPoint != -1) {
        def nameEndIndex = modifiedCmd.indexOf(' ', insertionPoint)
        if (nameEndIndex != -1) {
            def nextSpaceIndex = modifiedCmd.indexOf(' ', nameEndIndex + 1)
            if (nextSpaceIndex != -1) {
                modifiedCmd = modifiedCmd.substring(0, nextSpaceIndex) +
                        ' \\\n              --env-file ' + credentialEnvFile +
                        modifiedCmd.substring(nextSpaceIndex)
            }
        }
    }

    return modifiedCmd
}

/**
 * Cleanup credential environment file securely
 * Uses shred if available, falls back to rm
 *
 * @param credentialEnvFile File to cleanup
 */
def cleanupCredentialEnvFile(String credentialEnvFile) {
    if (!credentialEnvFile) {
        return
    }

    try {
        if (context.fileExists(credentialEnvFile)) {
            context.sh "shred -vfz -n 3 ${credentialEnvFile} 2>/dev/null || rm -f ${credentialEnvFile}"
            logDebug("Credential environment file securely shredded: ${credentialEnvFile}")
        }
    } catch (Exception e) {
        logWarning("Failed to cleanup credential environment file: ${e.message}")
    }
}

// ========================================
// ENVIRONMENT FILE GENERATION
// ========================================

/**
 * Generate common environment variables list
 * Returns map of environment variables common to both setup and destroy
 *
 * @param includeCredentials Whether to include AWS credentials (default: false, passed via withCredentials)
 * @return Map of environment variable key-value pairs
 */
def getCommonEnvironmentVariables(boolean includeCredentials = false) {
    def s3Config = getS3Config()

    def envVars = [
            'BUILD_NUMBER': context.env.BUILD_NUMBER,
            'JOB_NAME': context.env.JOB_NAME,
            'S3_BUCKET_NAME': s3Config.bucket,
            'S3_BUCKET_REGION': s3Config.region,
            'S3_KEY_PREFIX': s3Config.keyPrefix,
            'AWS_REGION': s3Config.region,
            'QA_INFRA_WORK_PATH': context.env.QA_INFRA_WORK_PATH,
            'TERRAFORM_VARS_FILENAME': context.env.TERRAFORM_VARS_FILENAME ?: 'cluster.tfvars',
    ]

    if (context.env.TF_WORKSPACE) {
        envVars['TF_WORKSPACE'] = context.env.TF_WORKSPACE
    }

    if (context.env.TARGET_WORKSPACE) {
        envVars['TARGET_WORKSPACE'] = context.env.TARGET_WORKSPACE
    }

    return envVars
}

// Make this library returnable
return this