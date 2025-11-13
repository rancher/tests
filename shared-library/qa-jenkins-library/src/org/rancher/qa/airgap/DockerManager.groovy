package org.rancher.qa.airgap

/**
 * Encapsulates Docker execution logic required by the airgap pipelines.
 */
class DockerManager implements Serializable {

    private static final long serialVersionUID = 1L

    private final def steps

    DockerManager(def steps) {
        this.steps = steps
    }

    void buildImage(String imageName, String dockerfile = 'tests/validation/Dockerfile.tofu.e2e') {
        logInfo("Building Docker image: ${imageName}")
        steps.dir('.') {
            try {
                steps.sh './tests/validation/configure.sh > /dev/null 2>&1 || true'
            } catch (Exception ignored) {
                logWarning('configure.sh not executed; continuing')
            }

            def buildDate = safeShell("date -u +'%Y-%m-%dT%H:%M:%SZ'")
            def vcsRef = safeShell('git rev-parse --short HEAD 2>/dev/null || echo "unknown"')

            steps.sh """
                docker build . \
                    -f ${dockerfile} \
                    -t ${imageName} \
                    --build-arg BUILD_DATE=${buildDate} \
                    --build-arg VCS_REF=${vcsRef} \
                    --label \"pipeline.build.number=${steps.env.BUILD_NUMBER}\" \
                    --label \"pipeline.job.name=${steps.env.JOB_NAME}\" \
                    --quiet
            """.stripIndent()
        }
    }

    void createSharedVolume(String volumeName) {
        logInfo("Creating shared Docker volume: ${volumeName}")
        steps.sh "docker volume create --name ${volumeName} || true"
    }

    void stageSshKeys(String volumeName, String sshDir = './tests/.ssh') {
        if (!volumeName) {
            logWarning('Cannot stage SSH keys without a volume name')
            return
        }
        if (!steps.fileExists(sshDir)) {
            logWarning("SSH directory not found at ${sshDir}; skipping key staging")
            return
        }
        logInfo('Copying SSH keys into shared Docker volume')
        steps.sh """
            docker run --rm \\
                -v ${volumeName}:/target \\
                -v \$(pwd)/${sshDir}:/source:ro \\
                alpine:latest \\
                sh -c '
                    if [ -d /source ] && [ -n "\$(ls -A /source 2>/dev/null)" ]; then
                        mkdir -p /target/.ssh
                        chmod 700 /target/.ssh
                        cp /source/* /target/.ssh/ || { echo "Failed to copy keys"; exit 1; }
                        chmod 600 /target/.ssh/* || true
                        echo "SSH keys copied successfully"
                    else
                        echo "No SSH keys found in source directory; skipping copy"
                    fi
                '
        """.stripIndent()
    }

    void cleanupResources(String imageName, String volumeName, String containerPattern) {
        logInfo('Cleaning up Docker resources')
        try {
            steps.sh """
                docker ps -aq --filter \"name=${containerPattern}\" | xargs -r docker stop || true
                docker ps -aq --filter \"name=${containerPattern}\" | xargs -r docker rm -v || true
                docker rmi -f ${imageName} || true
                docker volume rm -f ${volumeName} || true
                docker system prune -f || true
            """.stripIndent()
        } catch (Exception e) {
            logWarning("Docker cleanup encountered issues: ${e.message}")
        }
    }

    /**
     * Execute a shell script inside the build container, mounting workspace assets and credentials.
     */
    void executeScriptInContainer(Map opts) {
        def scriptContent = opts.script ?: steps.error('script content is required')
        def imageName = opts.image ?: steps.env.IMAGE_NAME ?: steps.error('IMAGE_NAME is required')
        def volumeName = opts.volume ?: steps.env.VALIDATION_VOLUME
        def containerName = opts.containerName ?: defaultContainerName()
        def envFile = opts.envFile ?: steps.env.ENV_FILE ?: '.env'
        def extraEnv = opts.extraEnv as Map ?: [:]
        def timeoutMinutes = (opts.timeout ?: 30) as int

        def scriptFile = writeTempScript(scriptContent)
        String credentialEnvFile = null
        try {
            validateDockerRuntime(imageName)
            steps.withCredentials(defaultCredentialBindings()) {
                credentialEnvFile = writeCredentialEnvFile()
                def dockerCmd = buildDockerCommand(imageName, volumeName, containerName, envFile, scriptFile, extraEnv, credentialEnvFile)
                steps.timeout(time: timeoutMinutes, unit: 'MINUTES') {
                    steps.sh dockerCmd
                }
            }
        } finally {
            cleanupTempFiles(scriptFile, credentialEnvFile)
        }
    }

    private String buildDockerCommand(String imageName, String volumeName, String containerName, String envFile, String scriptFile, Map extraEnv, String credentialEnvFile) {
        def mounts = [] as List<String>
        if (volumeName) {
            mounts << "-v ${volumeName}:/root"
        }
        def repoRoot = steps.pwd()
        if (steps.fileExists("${repoRoot}/tests")) {
            mounts << "-v ${repoRoot}/tests:/root/go/src/github.com/rancher/tests"
        } else {
            mounts << "-v ${repoRoot}:/root/go/src/github.com/rancher/tests"
        }
        if (steps.fileExists("${repoRoot}/qa-infra-automation")) {
            mounts << "-v ${repoRoot}/qa-infra-automation:/root/go/src/github.com/rancher/qa-infra-automation"
        }
        mounts << "-v ${repoRoot}/${scriptFile}:/tmp/script.sh"
        if (steps.fileExists("${repoRoot}/${envFile}")) {
            mounts << "-v ${repoRoot}/${envFile}:/tmp/.env"
        }

        def envArgs = [] as List<String>
        envArgs << '--env-file /tmp/.env'
        extraEnv.each { key, value ->
            if (value != null) {
                envArgs << "-e ${key}=${value}".toString()
            }
        }
        envArgs << "-e TF_WORKSPACE=${steps.env.TF_WORKSPACE ?: ''}"
        envArgs << "-e TERRAFORM_VARS_FILENAME=${steps.env.TERRAFORM_VARS_FILENAME ?: 'cluster.tfvars'}"

        def credentialFlag = credentialEnvFile ? " --env-file ${credentialEnvFile}" : ''
        def command = ['docker', 'run', '--rm']
        command += mounts.collect { ['-v', it] }.flatten()
        command += ['--name', containerName]
        command += envArgs
        command << imageName
        command << '/bin/bash'
        command << '/tmp/script.sh'

        def rendered = command.join(' ')
        return credentialFlag ? "${rendered}${credentialFlag}" : rendered
    }

    private String writeTempScript(String scriptContent) {
        def fileName = "docker-script-${System.currentTimeMillis()}.sh"
        steps.writeFile file: fileName, text: scriptContent
        return fileName
    }

    private void cleanupTempFiles(String scriptFile, String credentialEnvFile) {
        try {
            steps.sh "rm -f ${scriptFile} || true"
        } catch (Exception ignored) {
            logWarning("Failed to cleanup script file ${scriptFile}")
        }
        if (credentialEnvFile && steps.fileExists(credentialEnvFile)) {
            try {
                steps.sh "shred -vfz -n 3 ${credentialEnvFile} 2>/dev/null || rm -f ${credentialEnvFile}"
            } catch (Exception ignored) {
                steps.sh "rm -f ${credentialEnvFile} || true"
            }
        }
    }

    private void validateDockerRuntime(String imageName) {
        logInfo('Validating Docker runtime before execution')
        safeShell('docker --version')
        safeShell('docker info --format "Server Version: {{.ServerVersion}}"')
        safeShell('df -h /var/lib/docker 2>/dev/null || df -h /')
        def inspect = safeShell("docker image inspect ${imageName} --format='{{.Id}}' 2>/dev/null || echo 'NOT_FOUND'")
        if ('NOT_FOUND' == inspect.trim()) {
            logWarning("Docker image ${imageName} not present locally; docker run will pull it")
        }
    }

    private List defaultCredentialBindings() {
        return [
            steps.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
            steps.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY'),
            steps.string(credentialsId: 'AWS_SSH_PEM_KEY', variable: 'AWS_SSH_PEM_KEY'),
            steps.string(credentialsId: 'AWS_SSH_KEY_NAME', variable: 'AWS_SSH_KEY_NAME')
        ]
    }

    private String writeCredentialEnvFile() {
        def fileName = "docker-credentials-${System.currentTimeMillis()}.env"
        def content = [] as List<String>
        if (steps.env.AWS_ACCESS_KEY_ID) {
            content << "AWS_ACCESS_KEY_ID=${steps.env.AWS_ACCESS_KEY_ID}"
        }
        if (steps.env.AWS_SECRET_ACCESS_KEY) {
            content << "AWS_SECRET_ACCESS_KEY=${steps.env.AWS_SECRET_ACCESS_KEY}"
        }
        steps.writeFile file: fileName, text: content.join('\n')
        steps.sh "chmod 600 ${fileName}"
        return fileName
    }

    private String defaultContainerName() {
        def base = steps.env.BUILD_CONTAINER_NAME ?: 'rancher-ansible-airgap'
        "${base}-script-${System.currentTimeMillis()}"
    }

    private String safeShell(String command) {
        try {
            return steps.sh(script: command, returnStdout: true).trim()
        } catch (Exception e) {
            logWarning("Shell command failed: ${command} -> ${e.message}")
            return ''
        }
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
