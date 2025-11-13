import org.rancher.qa.airgap.ArtifactManager
import org.rancher.qa.airgap.DockerManager
import org.rancher.qa.airgap.EnvironmentManager
import org.rancher.qa.airgap.InfrastructureManager
import org.rancher.qa.airgap.PipelineDefaults
import org.rancher.qa.airgap.ValidationManager

/**
 * Shared library entry points for the airgap setup pipeline.
 */
class AirgapSetupPipeline implements Serializable {

    private static final long serialVersionUID = 1L

    private final def steps
    private final EnvironmentManager envManager
    private final DockerManager dockerManager
    private final InfrastructureManager infraManager
    private final ArtifactManager artifactManager
    private final ValidationManager validationManager

    private Map state = [:]

    AirgapSetupPipeline(def steps) {
        this.steps = steps
        this.envManager = new EnvironmentManager(steps)
        this.dockerManager = new DockerManager(steps)
        this.infraManager = new InfrastructureManager(dockerManager)
        this.artifactManager = new ArtifactManager(steps)
        this.validationManager = new ValidationManager(steps)
    }

    Map initialize(Map ctx = [:]) {
        logInfo('Initializing setup pipeline')
        steps.deleteDir()
        state = ctxWithDefaults(ctx)
        envManager.configureSetupEnvironment(state)
        syncEnvFromContext([
            'BUILD_CONTAINER_NAME',
            'IMAGE_NAME',
            'VALIDATION_VOLUME',
            'TF_WORKSPACE',
            'RKE2_VERSION',
            'RANCHER_VERSION',
            'HOSTNAME_PREFIX',
            'RANCHER_HOSTNAME'
        ])
        logInfo("Build container: ${state.BUILD_CONTAINER_NAME}")
        logInfo("Docker image: ${state.IMAGE_NAME}")
        logInfo("Volume: ${state.VALIDATION_VOLUME}")
        return state
    }

    void checkoutRepositories() {
        ensureState()
        logInfo('Checking out repositories')
        def testsTarget = state.testsTarget ?: './tests'
        def qaInfraTarget = state.qaInfraTarget ?: './qa-infra-automation'

        def checkoutExtensions = [
            [$class: 'CleanCheckout'],
            [$class: 'CloneOption', depth: 1, shallow: true]
        ]

        steps.dir(testsTarget) {
            steps.checkout([
                $class: 'GitSCM',
                branches: [[name: "*/${state.RANCHER_TEST_REPO_BRANCH ?: 'main'}"]],
                extensions: checkoutExtensions,
                userRemoteConfigs: [[url: state.RANCHER_TEST_REPO_URL ?: PipelineDefaults.DEFAULT_RANCHER_TEST_REPO]]
            ])
        }

        steps.dir(qaInfraTarget) {
            steps.checkout([
                $class: 'GitSCM',
                branches: [[name: "*/${state.QA_INFRA_REPO_BRANCH ?: 'main'}"]],
                extensions: checkoutExtensions,
                userRemoteConfigs: [[url: state.QA_INFRA_REPO_URL ?: PipelineDefaults.DEFAULT_QA_INFRA_REPO]]
            ])
        }
    }

    void prepareInfrastructure() {
        ensureState()
        validationManager.validatePipelineParameters(state)
        dockerManager.buildImage(state.IMAGE_NAME)
        dockerManager.createSharedVolume(state.VALIDATION_VOLUME)
        dockerManager.stageSshKeys(state.VALIDATION_VOLUME)
        validationManager.validateSensitiveDataHandling(state, false)
    }

    void deployInfrastructure() {
        ensureState()
        validationManager.ensureRequiredVariables(state, [
            'QA_INFRA_WORK_PATH',
            'TF_WORKSPACE',
            'TERRAFORM_VARS_FILENAME',
            'TERRAFORM_BACKEND_CONFIG_FILENAME'
        ])
        writeTofuConfiguration()
        infraManager.deployInfrastructure(extraEnv: buildInfrastructureEnv())
        artifactManager.extractFromVolume(state.VALIDATION_VOLUME)
    }

    void prepareAnsible() {
        ensureState()
        validationManager.ensureRequiredVariables(state, ['QA_INFRA_WORK_PATH', 'ANSIBLE_VARS_FILENAME'])
        infraManager.prepareAnsible(extraEnv: buildAnsibleEnv())
    }

    void deployRke2() {
        ensureState()
        validationManager.ensureRequiredVariables(state, ['QA_INFRA_WORK_PATH', 'ANSIBLE_VARS_FILENAME'])
        infraManager.deployRke2(extraEnv: buildRke2Env())
    }

    void deployRancher() {
        ensureState()
        validationManager.ensureRequiredVariables(state, ['QA_INFRA_WORK_PATH', 'ANSIBLE_VARS_FILENAME'])
        infraManager.deployRancher(extraEnv: buildRancherEnv())
    }

    void archiveAndCleanup(boolean destroyOnFailure = false) {
        ensureState()
        artifactManager.extractFromVolume(state.VALIDATION_VOLUME)
        artifactManager.archiveArtifacts(state.artifactPatterns ?: ['artifacts/**'])
        dockerManager.cleanupResources(state.IMAGE_NAME, state.VALIDATION_VOLUME, state.BUILD_CONTAINER_NAME)
        if (destroyOnFailure) {
            runCleanupScript('deployment_failure', true)
        }
    }

    void runCleanupScript(String reason, boolean destroyOnFailure = false) {
        ensureState()
        def script = """#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_cleanup.sh
perform_cleanup \"${reason}\" \"${state.TF_WORKSPACE}\" \"${destroyOnFailure}\"
""".stripIndent()
        dockerManager.executeScriptInContainer([
            script: script,
            extraEnv: [
                'QA_INFRA_WORK_PATH': state.QA_INFRA_WORK_PATH ?: '/root/go/src/github.com/rancher/qa-infra-automation',
                'TF_WORKSPACE': state.TF_WORKSPACE ?: ''
            ],
            timeout: PipelineDefaults.TERRAFORM_TIMEOUT_MINUTES
        ])
    }

    Map getState() {
        state
    }

    Map ctxWithDefaults(Map ctx) {
        ctx = new LinkedHashMap(ctx ?: [:])
        ctx.BUILD_CONTAINER_NAME = ctx.BUILD_CONTAINER_NAME ?: computeContainerName()
        ctx.IMAGE_NAME = ctx.IMAGE_NAME ?: computeImageName()
        ctx.VALIDATION_VOLUME = ctx.VALIDATION_VOLUME ?: computeVolumeName()
        ctx.ENV_FILE = ctx.ENV_FILE ?: '.env'
        ctx.TF_WORKSPACE = ctx.TF_WORKSPACE ?: "jenkins_airgap_ansible_workspace_${steps.env.BUILD_NUMBER ?: '0'}"
        ctx.TERRAFORM_BACKEND_CONFIG_FILENAME = ctx.TERRAFORM_BACKEND_CONFIG_FILENAME ?: 'backend.tf'
        return ctx
    }

    private void ensureState() {
        if (!state) {
            steps.error('Airgap setup pipeline state has not been initialized')
        }
    }

    private String computeContainerName() {
        "${PipelineDefaults.CONTAINER_NAME_PREFIX}-${steps.env.BUILD_NUMBER ?: '0'}"
    }

    private String computeImageName() {
        "rancher-ansible-airgap-setup-${steps.env.BUILD_NUMBER ?: '0'}"
    }

    private String computeVolumeName() {
        "${PipelineDefaults.SHARED_VOLUME_PREFIX}-${steps.env.BUILD_NUMBER ?: '0'}"
    }

    private void syncEnvFromContext(List<String> keys) {
        keys.each { key ->
            def value = state[key]
            if (value != null) {
                steps.env."${key}" = value.toString()
            }
        }
    }

    private Map buildInfrastructureEnv() {
        [
            'RKE2_VERSION'        : steps.env.RKE2_VERSION ?: state.RKE2_VERSION,
            'RANCHER_VERSION'     : steps.env.RANCHER_VERSION ?: state.RANCHER_VERSION,
            'HOSTNAME_PREFIX'     : steps.env.HOSTNAME_PREFIX ?: state.HOSTNAME_PREFIX,
            'RANCHER_HOSTNAME'    : steps.env.RANCHER_HOSTNAME ?: state.RANCHER_HOSTNAME,
            'PRIVATE_REGISTRY_URL': state.PRIVATE_REGISTRY_URL ?: '',
            'PRIVATE_REGISTRY_USERNAME': state.PRIVATE_REGISTRY_USERNAME ?: '',
            'UPLOAD_CONFIG_TO_S3' : 'true',
            'S3_BUCKET_NAME'      : state.S3_BUCKET_NAME ?: PipelineDefaults.DEFAULT_S3_BUCKET,
            'S3_BUCKET_REGION'    : state.S3_BUCKET_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION,
            'S3_KEY_PREFIX'       : state.S3_KEY_PREFIX ?: 'jenkins-airgap-rke2',
            'AWS_REGION'          : state.AWS_REGION ?: state.S3_BUCKET_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION,
            'AWS_SSH_PEM_KEY'     : steps.env.AWS_SSH_PEM_KEY,
            'AWS_SSH_KEY_NAME'    : steps.env.AWS_SSH_KEY_NAME
        ]
    }

    private Map buildAnsibleEnv() {
        [
            'ANSIBLE_VARIABLES'        : state.ANSIBLE_VARIABLES ?: '',
            'RKE2_VERSION'             : steps.env.RKE2_VERSION ?: state.RKE2_VERSION,
            'RANCHER_VERSION'          : steps.env.RANCHER_VERSION ?: state.RANCHER_VERSION,
            'HOSTNAME_PREFIX'          : steps.env.HOSTNAME_PREFIX ?: state.HOSTNAME_PREFIX,
            'RANCHER_HOSTNAME'         : steps.env.RANCHER_HOSTNAME ?: state.RANCHER_HOSTNAME,
            'PRIVATE_REGISTRY_URL'     : state.PRIVATE_REGISTRY_URL ?: '',
            'PRIVATE_REGISTRY_USERNAME': state.PRIVATE_REGISTRY_USERNAME ?: '',
            'PRIVATE_REGISTRY_PASSWORD': state.PRIVATE_REGISTRY_PASSWORD ?: '',
            'SKIP_YAML_VALIDATION'     : state.SKIP_YAML_VALIDATION ?: 'false',
            'AWS_SSH_PEM_KEY'          : steps.env.AWS_SSH_PEM_KEY,
            'AWS_SSH_KEY_NAME'         : steps.env.AWS_SSH_KEY_NAME
        ]
    }

    private Map buildRke2Env() {
        [
            'RKE2_VERSION'    : steps.env.RKE2_VERSION ?: state.RKE2_VERSION,
            'SKIP_VALIDATION' : 'false',
            'AWS_SSH_PEM_KEY' : steps.env.AWS_SSH_PEM_KEY,
            'AWS_SSH_KEY_NAME': steps.env.AWS_SSH_KEY_NAME
        ]
    }

    private Map buildRancherEnv() {
        [
            'RANCHER_VERSION': steps.env.RANCHER_VERSION ?: state.RANCHER_VERSION,
            'HOSTNAME_PREFIX': steps.env.HOSTNAME_PREFIX ?: state.HOSTNAME_PREFIX,
            'RANCHER_HOSTNAME': steps.env.RANCHER_HOSTNAME ?: state.RANCHER_HOSTNAME,
            'SKIP_VERIFICATION': 'false'
        ]
    }

    private void writeTofuConfiguration() {
        logInfo('Generating Terraform configuration')
        def bucket = state.S3_BUCKET_NAME ?: PipelineDefaults.DEFAULT_S3_BUCKET
        def region = state.S3_BUCKET_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION
        def keyPrefix = state.S3_KEY_PREFIX ?: 'jenkins-airgap-rke2'

        steps.sh 'mkdir -p qa-infra-automation/tofu/aws/modules/airgap'

        def wroteVars = false
        def terraformConfig = state.TERRAFORM_CONFIG
        if (terraformConfig && terraformConfig.trim()) {
            def rendered = terraformConfig
            if (steps.env.AWS_SECRET_ACCESS_KEY) {
                rendered = rendered.replace('${AWS_SECRET_ACCESS_KEY}', steps.env.AWS_SECRET_ACCESS_KEY)
            }
            if (steps.env.AWS_ACCESS_KEY_ID) {
                rendered = rendered.replace('${AWS_ACCESS_KEY_ID}', steps.env.AWS_ACCESS_KEY_ID)
            }
            if (state.HOSTNAME_PREFIX) {
                rendered = rendered.replace('${HOSTNAME_PREFIX}', state.HOSTNAME_PREFIX)
            }
            steps.dir('qa-infra-automation/tofu/aws/modules/airgap') {
                steps.writeFile file: state.TERRAFORM_VARS_FILENAME ?: 'cluster.tfvars', text: rendered
                wroteVars = true
            }
        }

        if (!wroteVars) {
            steps.error('TERRAFORM_CONFIG is required to generate cluster.tfvars')
        }

        steps.dir('qa-infra-automation/tofu/aws/modules/airgap') {
            def backendConfig = """
terraform {
  backend "s3" {
    bucket = "${bucket}"
    key    = "${keyPrefix}"
    region = "${region}"
  }
}
"""
            steps.writeFile file: state.TERRAFORM_BACKEND_CONFIG_FILENAME ?: 'backend.tf', text: backendConfig.stripIndent()
        }
    }

    private void logInfo(String msg) {
        steps.echo "${PipelineDefaults.LOG_PREFIX_INFO} ${timestamp()} ${msg}"
    }

    private static String timestamp() {
        new Date().format('yyyy-MM-dd HH:mm:ss')
    }

}

def pipeline(def steps = this) {
    new AirgapSetupPipeline(steps)
}
