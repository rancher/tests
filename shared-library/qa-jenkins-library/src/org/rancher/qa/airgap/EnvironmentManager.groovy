package org.rancher.qa.airgap

/**
 * Handles airgap pipeline environment preparation logic that previously lived in the Jenkinsfiles.
 */
class EnvironmentManager implements Serializable {

    private static final long serialVersionUID = 1L

    private final def steps

    EnvironmentManager(def steps) {
        this.steps = steps
    }

    void configureSetupEnvironment(Map ctx) {
        logInfo('Configuring complete environment setup')

        readAndValidateAnsibleVariables(ctx)

        ctx.RKE2_VERSION = ctx.RKE2_VERSION ?: PipelineDefaults.DEFAULT_RKE2_VERSION
        ctx.RANCHER_VERSION = ctx.RANCHER_VERSION ?: PipelineDefaults.DEFAULT_RANCHER_VERSION
        ctx.TERRAFORM_VARS_FILENAME = 'cluster.tfvars'
        ctx.RANCHER_HOSTNAME = ((ctx.HOSTNAME_PREFIX ?: PipelineDefaults.DEFAULT_HOSTNAME_PREFIX) + '.qa.rancher.space')
        applyEnv(ctx, 'RKE2_VERSION')
        applyEnv(ctx, 'RANCHER_VERSION')
        applyEnv(ctx, 'HOSTNAME_PREFIX')
        applyEnv(ctx, 'RANCHER_HOSTNAME')
        applyEnv(ctx, 'TERRAFORM_VARS_FILENAME')

        steps.withCredentials(resolveCredentialBindings()) {
            setupSSHKeys(ctx)
            generateEnvFile(ctx)
        }

        logInfo('Environment configuration completed successfully')
    }

    void configureDestroyEnvironment(Map ctx) {
        logInfo('Configuring destruction environment setup')

        steps.withCredentials(resolveCredentialBindings()) {
            ensureDestroySshKeys(ctx)
            generateDestroyEnvFile(ctx)
        }

        logInfo('Destruction environment configuration completed successfully')
    }

    private void readAndValidateAnsibleVariables(Map ctx) {
        def raw = ctx.ANSIBLE_VARIABLES ?: ''
        if (!raw.trim()) {
            steps.error('ANSIBLE_VARIABLES parameter is required but was not provided')
        }
        ctx.ANSIBLE_VARIABLES = raw.trim()
        logInfo("Ansible variables loaded: ${ctx.ANSIBLE_VARIABLES.length()} bytes")
    }

    private void setupSSHKeys(Map ctx) {
        logInfo('Setting up SSH keys')
        def sshDir = './tests/.ssh'

        if (!steps.env.AWS_SSH_PEM_KEY || !steps.env.AWS_SSH_KEY_NAME) {
            logWarning('SSH key credentials not available; generating ephemeral keypair')
            generateEphemeralKey(ctx, sshDir)
            return
        }

        steps.dir(sshDir) {
            steps.sh 'mkdir -p . && chmod 700 .'
            def decodedKey = new String(steps.env.AWS_SSH_PEM_KEY.decodeBase64())
            steps.writeFile file: steps.env.AWS_SSH_KEY_NAME, text: decodedKey
            steps.sh "chmod 600 ${steps.env.AWS_SSH_KEY_NAME}"
            steps.sh "chown \$(whoami):\$(whoami) ${steps.env.AWS_SSH_KEY_NAME} 2>/dev/null || true"
            steps.sh "ssh-keygen -f ${steps.env.AWS_SSH_KEY_NAME} -y > ${steps.env.AWS_SSH_KEY_NAME}.pub || true"
            steps.sh "chmod 644 ${steps.env.AWS_SSH_KEY_NAME}.pub 2>/dev/null || true"
        }

        ctx.SSH_KEY_PATH = "${sshDir}/${steps.env.AWS_SSH_KEY_NAME}"
        ctx.AWS_SSH_KEY_NAME = steps.env.AWS_SSH_KEY_NAME
        applyEnvValue('SSH_KEY_PATH', ctx.SSH_KEY_PATH)
        logInfo('SSH keys configured successfully')
    }

    private void generateEnvFile(Map ctx) {
        def path = ctx.ENV_FILE ?: '.env'
        def lines = [] as List<String>
        lines << '# Environment Configuration - credentials sourced via Jenkins credentials binding'
        lines << "TF_WORKSPACE=${ctx.TF_WORKSPACE ?: ''}"
        lines << "BUILD_NUMBER=${steps.env.BUILD_NUMBER ?: ''}"
        lines << "JOB_NAME=${steps.env.JOB_NAME ?: ''}"
        lines << "TERRAFORM_TIMEOUT=${ctx.TERRAFORM_TIMEOUT ?: PipelineDefaults.TERRAFORM_TIMEOUT_MINUTES}"
        lines << "ANSIBLE_TIMEOUT=${ctx.ANSIBLE_TIMEOUT ?: PipelineDefaults.ANSIBLE_TIMEOUT_MINUTES}"
        lines << "QA_INFRA_WORK_PATH=${ctx.QA_INFRA_WORK_PATH ?: '/root/go/src/github.com/rancher/qa-infra-automation'}"
        lines << "TERRAFORM_VARS_FILENAME=${ctx.TERRAFORM_VARS_FILENAME ?: 'cluster.tfvars'}"
        lines << "ANSIBLE_VARS_FILENAME=${ctx.ANSIBLE_VARS_FILENAME ?: 'vars.yaml'}"
        lines << "RKE2_VERSION=${ctx.RKE2_VERSION ?: PipelineDefaults.DEFAULT_RKE2_VERSION}"
        lines << "RANCHER_VERSION=${ctx.RANCHER_VERSION ?: PipelineDefaults.DEFAULT_RANCHER_VERSION}"
        lines << "HOSTNAME_PREFIX=${ctx.HOSTNAME_PREFIX ?: PipelineDefaults.DEFAULT_HOSTNAME_PREFIX}"
        lines << "RANCHER_HOSTNAME=${ctx.RANCHER_HOSTNAME ?: ''}"
        lines << "PRIVATE_REGISTRY_URL=${ctx.PRIVATE_REGISTRY_URL ?: ''}"
        lines << "PRIVATE_REGISTRY_USERNAME=${ctx.PRIVATE_REGISTRY_USERNAME ?: ''}"
        lines << ''
        lines << '# Ansible configuration'
        lines << "ANSIBLE_VARIABLES=${ctx.ANSIBLE_VARIABLES ?: ''}"
        lines << ''
        lines << '# AWS configuration'
        lines << "AWS_REGION=${ctx.AWS_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION}"
        lines << "AWS_SSH_KEY_NAME=${steps.env.AWS_SSH_KEY_NAME ?: ''}"
        lines << ''
        lines << '# S3 backend configuration'
        lines << "S3_BUCKET_NAME=${ctx.S3_BUCKET_NAME ?: PipelineDefaults.DEFAULT_S3_BUCKET}"
        lines << "S3_BUCKET_REGION=${ctx.S3_BUCKET_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION}"
        lines << "S3_KEY_PREFIX=${ctx.S3_KEY_PREFIX ?: 'jenkins-airgap-rke2'}"

        steps.writeFile file: path, text: lines.join('\n')
        logInfo("Environment file created: ${path}")
        ctx.ENV_FILE = path
        applyEnv(ctx, 'ENV_FILE')
    }

    private void generateDestroyEnvFile(Map ctx) {
        def path = ctx.ENV_FILE ?: '.env'
        def pipelineParams = resolvePipelineParams()
        def s3BucketName = ctx.S3_BUCKET_NAME ?: pipelineParams.S3_BUCKET_NAME ?: 'jenkins-terraform-state-storage'
        def s3KeyPrefix = ctx.S3_KEY_PREFIX ?: pipelineParams.S3_KEY_PREFIX ?: 'jenkins-airgap-rke2/terraform.tfstate'
        def s3BucketRegion = ctx.S3_BUCKET_REGION ?: pipelineParams.S3_BUCKET_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION
        def awsRegion = ctx.AWS_REGION ?: pipelineParams.AWS_REGION ?: s3BucketRegion
        def workspace = ctx.TARGET_WORKSPACE ?: ctx.TF_WORKSPACE ?: pipelineParams.TARGET_WORKSPACE ?: ''

        if (workspace?.trim()) {
            if (!(s3KeyPrefix?.startsWith('env:/'))) {
                def parts = s3KeyPrefix.tokenize('/')
                def baseKey = parts ? parts[-1] : s3KeyPrefix
                s3KeyPrefix = "env:/${workspace}/${baseKey}"
                logInfo("Normalized S3_KEY_PREFIX to workspace-aware value: '${s3KeyPrefix}'")
            } else {
                logInfo("S3_KEY_PREFIX already workspace-scoped: '${s3KeyPrefix}'")
            }
        }

        ctx.S3_BUCKET_NAME = s3BucketName
        ctx.S3_KEY_PREFIX = s3KeyPrefix
        ctx.S3_BUCKET_REGION = s3BucketRegion
        ctx.AWS_REGION = awsRegion
        ctx.QA_INFRA_WORK_PATH = ctx.QA_INFRA_WORK_PATH ?: '/root/go/src/github.com/rancher/qa-infra-automation'
        ctx.TERRAFORM_VARS_FILENAME = ctx.TERRAFORM_VARS_FILENAME ?: 'cluster.tfvars'
        applyEnv(ctx, 'S3_BUCKET_NAME')
        applyEnv(ctx, 'S3_KEY_PREFIX')
        applyEnv(ctx, 'S3_BUCKET_REGION')
        applyEnv(ctx, 'AWS_REGION')
        applyEnv(ctx, 'QA_INFRA_WORK_PATH')
        applyEnv(ctx, 'TERRAFORM_VARS_FILENAME')

        def lines = [] as List<String>
        lines << '# Environment variables for infrastructure destruction containers'
        lines << '# NOTE: All sensitive credentials are passed via Jenkins withCredentials block for security'
        lines << "TARGET_WORKSPACE=${ctx.TARGET_WORKSPACE ?: ''}"
        lines << "BUILD_NUMBER=${steps.env.BUILD_NUMBER ?: ''}"
        lines << "JOB_NAME=${steps.env.JOB_NAME ?: ''}"
        lines << "QA_INFRA_WORK_PATH=${ctx.QA_INFRA_WORK_PATH}"
        lines << "TERRAFORM_VARS_FILENAME=${ctx.TERRAFORM_VARS_FILENAME}"
        lines << "S3_BUCKET_NAME=${s3BucketName}"
        lines << "S3_KEY_PREFIX=${s3KeyPrefix}"
        lines << "S3_BUCKET_REGION=${s3BucketRegion}"
        lines << "AWS_REGION=${awsRegion}"
        lines << ''
        lines << '# AWS Credentials excluded - will be passed via withCredentials'
        lines << ''
        lines << '# Terraform Variables for OpenTofu (TF_VAR_ prefix for automatic variable population)'
        lines << "TF_VAR_aws_region=${awsRegion}"

        steps.writeFile file: path, text: lines.join('\n')
        ctx.ENV_FILE = path
        applyEnv(ctx, 'ENV_FILE')

        try {
            def preview = steps.readFile(file: path).split('\n')
            def limit = Math.min(preview.length, 10)
            for (int i = 0; i < limit; i++) {
                logInfo(preview[i])
            }
        } catch (Exception ignored) {
            logWarning('Unable to read environment file preview')
        }
    }

    void cleanupSSHKeys(String awsSshKeyName = steps.env.AWS_SSH_KEY_NAME) {
        logInfo('Cleaning up SSH keys securely')
        def sshDir = './tests/.ssh'
        if (!awsSshKeyName) {
            return
        }

        def keyPath = "${sshDir}/${awsSshKeyName}"
        if (steps.fileExists(keyPath)) {
            try {
                steps.sh "shred -vfz -n 3 ${keyPath} 2>/dev/null || rm -f ${keyPath}"
                logInfo("SSH key securely shredded: ${keyPath}")
            } catch (Exception ignored) {
                steps.sh "rm -f ${keyPath}"
                logWarning("SSH key deleted via rm (shred unavailable): ${keyPath}")
            }
        }

        steps.sh "rm -f ${sshDir}/known_hosts ${sshDir}/config 2>/dev/null || true"
        if (steps.fileExists(sshDir)) {
            steps.sh "chmod 700 ${sshDir} 2>/dev/null || true"
        }
        logInfo('SSH key cleanup completed')
    }

    private void generateEphemeralKey(Map ctx, String sshDir) {
        def keyName = 'ephemeral_jenkins_key'
        steps.dir(sshDir) {
            steps.sh '''
                mkdir -p . && chmod 700 .
                ssh-keygen -t rsa -b 2048 -N "" -f ephemeral_jenkins_key -q || true
                chmod 600 ephemeral_jenkins_key || true
                chmod 644 ephemeral_jenkins_key.pub || true
            '''
        }
        steps.env.AWS_SSH_KEY_NAME = keyName
        ctx.AWS_SSH_KEY_NAME = keyName
        ctx.SSH_KEY_PATH = "${sshDir}/${keyName}"
        applyEnvValue('SSH_KEY_PATH', ctx.SSH_KEY_PATH)
        logInfo("Ephemeral SSH key generated at ${ctx.SSH_KEY_PATH}")
    }

    void ensureDestroySshKeys(Map ctx) {
        if (!steps.env.AWS_SSH_PEM_KEY || !steps.env.AWS_SSH_KEY_NAME) {
            logWarning('SSH key credentials not available; skipping destroy SSH key setup')
            return
        }

        logInfo('Setting up SSH keys for destruction workflow')
        def sshDir = './tests/.ssh'
        steps.dir(sshDir) {
            steps.sh 'mkdir -p . && chmod 700 .'
            def decodedKey = new String(steps.env.AWS_SSH_PEM_KEY.decodeBase64())
            steps.writeFile file: steps.env.AWS_SSH_KEY_NAME, text: decodedKey
            steps.sh "chmod 600 ${steps.env.AWS_SSH_KEY_NAME}"
        }
        ctx.SSH_KEY_PATH = "${sshDir}/${steps.env.AWS_SSH_KEY_NAME}"
        ctx.AWS_SSH_KEY_NAME = steps.env.AWS_SSH_KEY_NAME
        applyEnvValue('SSH_KEY_PATH', ctx.SSH_KEY_PATH)
        logInfo('SSH keys configured successfully for destruction workflow')
    }

    private Map resolvePipelineParams() {
        try {
            def params = steps.params
            return params instanceof Map ? params : [:]
        } catch (MissingPropertyException ignored) {
            return [:]
        }
    }

    private void applyEnv(Map ctx, String key) {
        def value = ctx[key]
        if (value != null) {
            applyEnvValue(key, value.toString())
        }
    }

    private void applyEnvValue(String key, String value) {
        if (value != null) {
            steps.env."${key}" = value
        }
    }

    private List resolveCredentialBindings(boolean includeSsh = true) {
        def bindings = [
            steps.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
            steps.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY')
        ]
        if (includeSsh) {
            bindings += [
                steps.string(credentialsId: 'AWS_SSH_PEM_KEY', variable: 'AWS_SSH_PEM_KEY'),
                steps.string(credentialsId: 'AWS_SSH_KEY_NAME', variable: 'AWS_SSH_KEY_NAME')
            ]
        }
        return bindings
    }

    private void logInfo(String msg) {
        steps.echo "${PipelineDefaults.LOG_PREFIX_INFO} ${timestamp()} ${msg}"
    }

    private void logWarning(String msg) {
        steps.echo "${PipelineDefaults.LOG_PREFIX_WARNING} ${timestamp()} ${msg}"
    }

    private static String timestamp() {
        new Date().format('yyyy-MM-dd HH:mm:ss')
    }

}
