import org.rancher.qa.airgap.ArtifactManager
import org.rancher.qa.airgap.DockerManager
import org.rancher.qa.airgap.EnvironmentManager
import org.rancher.qa.airgap.InfrastructureManager
import org.rancher.qa.airgap.PipelineDefaults
import org.rancher.qa.airgap.ValidationManager

class AirgapDestroyPipeline implements Serializable {

    private static final long serialVersionUID = 1L

    private final def steps
    private final EnvironmentManager envManager
    private final DockerManager dockerManager
    private final InfrastructureManager infraManager
    private final ArtifactManager artifactManager
    private final ValidationManager validationManager

    private Map state = [:]

    AirgapDestroyPipeline(def steps) {
        this.steps = steps
        this.envManager = new EnvironmentManager(steps)
        this.dockerManager = new DockerManager(steps)
        this.infraManager = new InfrastructureManager(dockerManager)
        this.artifactManager = new ArtifactManager(steps)
        this.validationManager = new ValidationManager(steps)
    }

    Map initialize(Map ctx = [:]) {
        logInfo('Initializing destroy pipeline')
        steps.deleteDir()
        state = ctxWithDefaults(ctx)
        envManager.configureDestroyEnvironment(state)
        syncEnvFromContext(['BUILD_CONTAINER_NAME', 'IMAGE_NAME', 'VALIDATION_VOLUME', 'TF_WORKSPACE'])
        logInfo("Build container: ${state.BUILD_CONTAINER_NAME}")
        logInfo("Docker image: ${state.IMAGE_NAME}")
        logInfo("Volume: ${state.VALIDATION_VOLUME}")
        state.CONTAINER_PREPARED = false
        return state
    }

    void checkoutRepositories() {
        ensureState()
        logInfo('Checking out repositories')
        def testsRepoBranch = this.@state.RANCHER_TEST_REPO_BRANCH ?: 'main'
        def testsRepoUrl = this.@state.RANCHER_TEST_REPO_URL ?: PipelineDefaults.DEFAULT_RANCHER_TEST_REPO
        def infraRepoBranch = this.@state.QA_INFRA_REPO_BRANCH ?: 'main'
        def infraRepoUrl = this.@state.QA_INFRA_REPO_URL ?: PipelineDefaults.DEFAULT_QA_INFRA_REPO
        def checkoutExtensions = [
            [$class: 'CleanCheckout'],
            [$class: 'CloneOption', depth: 1, shallow: true]
        ]

        steps.dir('./tests') {
            steps.checkout([
                $class: 'GitSCM',
                branches: [[name: "*/${testsRepoBranch}"]],
                extensions: checkoutExtensions,
                userRemoteConfigs: [[url: testsRepoUrl]]
            ])
        }

        steps.dir('./qa-infra-automation') {
            steps.checkout([
                $class: 'GitSCM',
                branches: [[name: "*/${infraRepoBranch}"]],
                extensions: checkoutExtensions,
                userRemoteConfigs: [[url: infraRepoUrl]]
            ])
        }
    }

    void destroyInfrastructure() {
        ensureState()
        if (!this.@state.CONTAINER_PREPARED) {
            prepareContainerResources()
        }
        validationManager.ensureRequiredVariables(state, ['QA_INFRA_WORK_PATH', 'TERRAFORM_VARS_FILENAME', 'ENV_FILE'])
        infraManager.destroyInfrastructure(extraEnv: makeDestroyEnv())
    }

    void prepareContainerResources() {
        ensureState()
        if (this.@state.CONTAINER_PREPARED) {
            logInfo('Container resources already prepared; skipping rebuild')
            return
        }

        logInfo('Preparing container resources for destroy workflow')
        def imageName = this.@state.IMAGE_NAME
        def volumeName = this.@state.VALIDATION_VOLUME
        dockerManager.buildImage(imageName)
        dockerManager.createSharedVolume(volumeName)

        steps.withCredentials(defaultCredentialBindings()) {
            envManager.ensureDestroySshKeys(this.@state)
            dockerManager.stageSshKeys(volumeName)
        }

        this.@state.CONTAINER_PREPARED = true
        logInfo('Container resources ready')
    }

    void cleanup(boolean cleanupS3 = false) {
        ensureState()
        artifactManager.extractFromVolume(state.VALIDATION_VOLUME)
        artifactManager.archiveArtifacts(state.artifactPatterns ?: [
            'destruction-summary.json',
            'destruction-plan.txt',
            'infrastructure-cleanup-report.txt',
            'artifacts/**'
        ])
        dockerManager.cleanupResources(state.IMAGE_NAME, state.VALIDATION_VOLUME, state.BUILD_CONTAINER_NAME)
        if (cleanupS3) {
            cleanupS3Workspace()
        }
    }

    void runCleanupScript(String reason) {
        ensureState()
        def script = """#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_cleanup.sh
perform_cleanup \"${reason}\" \"${state.TF_WORKSPACE}\" \"true\"
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
        ctx.TF_WORKSPACE = ctx.TF_WORKSPACE ?: ctx.TARGET_WORKSPACE ?: ''
        ctx.TERRAFORM_VARS_FILENAME = ctx.TERRAFORM_VARS_FILENAME ?: 'cluster.tfvars'
        return ctx
    }

    private void ensureState() {
        if (!state) {
            steps.error('Airgap destroy pipeline state has not been initialized')
        }
    }

    private String computeContainerName() {
        "${PipelineDefaults.CONTAINER_NAME_PREFIX}-${steps.env.BUILD_NUMBER ?: '0'}-destroy"
    }

    private String computeImageName() {
        "rancher-destroy-${steps.env.BUILD_NUMBER ?: '0'}"
    }

    private String computeVolumeName() {
        "DestroySharedVolume-${steps.env.BUILD_NUMBER ?: '0'}"
    }

    private void syncEnvFromContext(List<String> keys) {
        keys.each { key ->
            def value = this.@state[key]
            if (value != null) {
                steps.env."${key}" = value.toString()
            }
        }
    }

    private Map makeDestroyEnv() {
        [
            'QA_INFRA_WORK_PATH': state.QA_INFRA_WORK_PATH ?: '/root/go/src/github.com/rancher/qa-infra-automation',
            'TF_WORKSPACE'      : state.TF_WORKSPACE ?: '',
            'S3_BUCKET_NAME'    : state.S3_BUCKET_NAME ?: PipelineDefaults.DEFAULT_S3_BUCKET,
            'S3_BUCKET_REGION'  : state.S3_BUCKET_REGION ?: PipelineDefaults.DEFAULT_S3_BUCKET_REGION,
            'S3_KEY_PREFIX'     : state.S3_KEY_PREFIX ?: 'jenkins-airgap-rke2/terraform.tfstate'
        ]
    }

    private void cleanupS3Workspace() {
        def currentState = this.@state
        def required = ['S3_BUCKET_NAME', 'S3_BUCKET_REGION', 'S3_KEY_PREFIX', 'TF_WORKSPACE']
        def missing = required.findAll { !currentState[it] }
        if (missing) {
            def message = "Skipping S3 cleanup due to missing variables: ${missing.join(', ')}"
            steps.echo "${PipelineDefaults.LOG_PREFIX_WARNING} ${timestamp()} ${message}"
            return
        }

        def bucket = currentState.S3_BUCKET_NAME
        def region = currentState.S3_BUCKET_REGION
        def workspace = currentState.TF_WORKSPACE

        steps.withCredentials([
                steps.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
                steps.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY')
            ]) {
                def script = """#!/bin/bash
set -euo pipefail

docker run --rm \\
    -e AWS_ACCESS_KEY_ID=\"${'$'}{AWS_ACCESS_KEY_ID}\" \\
    -e AWS_SECRET_ACCESS_KEY=\"${'$'}{AWS_SECRET_ACCESS_KEY}\" \\
    -e AWS_DEFAULT_REGION=\"${region}\" \\
    amazon/aws-cli:latest \\
    sh -c 'set -euo pipefail
    if aws s3 ls "s3://${bucket}/env:/${workspace}/" --region "${region}" 2>/dev/null; then
        echo "Workspace directory found: s3://${bucket}/env:/${workspace}/"
        aws s3 ls "s3://${bucket}/env:/${workspace}/" --recursive --region "${region}" || true
        aws s3 rm "s3://${bucket}/env:/${workspace}/" --recursive --region "${region}"
        if aws s3 ls "s3://${bucket}/env:/${workspace}/" --region "${region}" 2>/dev/null; then
        echo "ERROR: Failed to delete workspace directory" >&2
        exit 1
    else
            echo "[OK] Successfully deleted workspace directory: s3://${bucket}/env:/${workspace}/"
    fi
else
    echo "[INFO] Workspace directory does not exist in S3 - nothing to clean up"
fi'
"""
            steps.sh(label: 'Cleanup S3 workspace directory', script: script)
            }
    }

    private void logInfo(String msg) {
        steps.echo "${PipelineDefaults.LOG_PREFIX_INFO} ${timestamp()} ${msg}"
    }

    private List defaultCredentialBindings() {
        [
            steps.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
            steps.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY'),
            steps.string(credentialsId: 'AWS_SSH_PEM_KEY', variable: 'AWS_SSH_PEM_KEY'),
            steps.string(credentialsId: 'AWS_SSH_KEY_NAME', variable: 'AWS_SSH_KEY_NAME')
        ]
    }

    private static String timestamp() {
        new Date().format('yyyy-MM-dd HH:mm:ss')
    }

}

def pipeline(def steps = this) {
    new AirgapDestroyPipeline(steps)
}
