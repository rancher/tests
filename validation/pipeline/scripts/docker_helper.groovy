// Shared Docker execution utilities for Jenkins airgap pipelines.
// Provides helper functions to run scripts inside the build container with
// consistent logging, masking, and credential handling.

class DockerExecutionHelper implements Serializable {

    private final def pipeline

    DockerExecutionHelper(def pipeline) {
        this.pipeline = pipeline
    }

    void executeScriptInContainer(String scriptContent, Map extraEnv = [:], boolean skipWorkspaceEnv = false) {
        def timestamp = System.currentTimeMillis()
        def containerName = "${pipeline.env.BUILD_CONTAINER_NAME}-script-${timestamp}"
        def scriptFile = "docker-script-${timestamp}.sh"

        pipeline.writeFile file: scriptFile, text: scriptContent

        def envVars = prepareEnvironmentVariables(extraEnv)
        def workspaceEnv = skipWorkspaceEnv ? '' : buildWorkspaceEnvFlags()
        def dockerCmd = prepareDockerCommand(containerName, scriptFile, envVars, workspaceEnv)

        executeDockerCommand(dockerCmd, scriptFile, extraEnv)
    }

    private String buildWorkspaceEnvFlags() {
        return " -e TF_WORKSPACE=${pipeline.env.TF_WORKSPACE} -e TERRAFORM_VARS_FILENAME=${pipeline.env.TERRAFORM_VARS_FILENAME}"
    }

    private String prepareEnvironmentVariables(Map extraEnv) {
        def envFlags = new StringBuilder()

        if (pipeline.env.DEBUG) {
            def escaped = escapeForShell(pipeline.env.DEBUG)
            envFlags.append(" -e \"DEBUG=${escaped}\"")
        }

        extraEnv.each { key, value ->
            if (value != null) {
                def strVal = value.toString()
                if (strVal.trim()) {
                    def escaped = escapeForShell(strVal)
                    envFlags.append(" -e \"${key}=${escaped}\"")
                }
            }
        }

        return envFlags.toString()
    }

    private String prepareDockerCommand(String containerName, String scriptFile, String envVars, String workspaceEnv) {
        def envFilePath = "${pipeline.pwd()}/${pipeline.env.ENV_FILE}"
        def inventoryMount = resolveInventoryMount()

        if (!pipeline.env.IMAGE_NAME) {
            pipeline.error 'ERROR: IMAGE_NAME environment variable is not set'
        }

        def escapedContainerName = containerName.replaceAll('[^a-zA-Z0-9_-]', '_')

        if (pipeline.env.DEBUG?.toBoolean()) {
            prepareDebugHelper()
        }

    def volumeMounts = collectVolumeMounts(envFilePath, scriptFile, inventoryMount)
    def volumeMountStr = volumeMounts.join(' \\\n+            ')

    def baseCmd = """docker run --rm \\\n+            ${volumeMountStr} \\\n+            --name ${escapedContainerName} \\\n+            -e QA_INFRA_WORK_PATH=/root/go/src/github.com/rancher/qa-infra-automation${workspaceEnv} \\\n+            ${envVars} \\\n+            \"${pipeline.env.IMAGE_NAME.trim()}\"""

        def executionCmd = buildPrimaryExecutionCommand()
        if (!pipeline.env.DEBUG?.toBoolean()) {
            executionCmd = """/bin/bash -c 'exec /bin/bash /tmp/script.sh'"""
        }

        return "${baseCmd} ${executionCmd}"
    }

    private void executeDockerCommand(String dockerCmd, String scriptFile, Map extraEnv) {
        def dockerSuccess = false
        String credentialEnvFile = null

        validateDockerEnvironment()

        pipeline.logInfo('Executing Docker command (sensitive data masked):')
        pipeline.logInfo(maskSensitiveData(dockerCmd))

        pipeline.withCredentials(credentialsBindings()) {
            credentialEnvFile = writeCredentialEnvironmentFile()

            try {
                dockerSuccess = attemptPrimaryExecution(dockerCmd, credentialEnvFile)
            } catch (Exception primaryException) {
                dockerSuccess = attemptFallbackExecution(primaryException, scriptFile, extraEnv, credentialEnvFile)
            }
        }

        if (!dockerSuccess) {
            pipeline.error('All Docker execution attempts failed')
        }

        cleanupExecutionArtifacts(scriptFile, credentialEnvFile)
    }

    private boolean attemptPrimaryExecution(String dockerCmd, String credentialEnvFile) {
        pipeline.logInfo('Attempting Docker execution with environment file mounting...')

        def modifiedDockerCmd = addCredentialEnvFile(dockerCmd, credentialEnvFile)

        pipeline.timeout(time: 30, unit: 'MINUTES') {
            pipeline.sh modifiedDockerCmd
        }

        pipeline.logInfo('[OK] Docker command executed successfully with environment file mounting')
        return true
    }

    private boolean attemptFallbackExecution(Exception primaryException, String scriptFile, Map extraEnv, String credentialEnvFile) {
        pipeline.logWarning("Primary Docker command failed: ${primaryException.message}")
        cleanupDanglingContainers()

        try {
            pipeline.logInfo('Attempting Docker execution with direct environment variables...')
            def fallbackCmd = prepareFallbackDockerCommand(scriptFile, extraEnv)
            def modifiedFallbackCmd = addCredentialEnvFile(fallbackCmd, credentialEnvFile)
            pipeline.logInfo('Executing fallback docker command (sensitive data masked):')
            pipeline.logInfo(maskSensitiveData(modifiedFallbackCmd))

            pipeline.timeout(time: 30, unit: 'MINUTES') {
                pipeline.sh modifiedFallbackCmd
            }

            pipeline.logInfo('[OK] Fallback Docker command executed successfully with direct environment variables')
            return true
        } catch (Exception fallbackException) {
            pipeline.logError('All Docker execution approaches failed:')
            pipeline.logError("  Primary (env file): ${primaryException.message}")
            pipeline.logError("  Fallback (direct env): ${fallbackException.message}")

            provideDockerDiagnostics()
            throw fallbackException
        }
    }

    private void cleanupExecutionArtifacts(String scriptFile, String credentialEnvFile) {
        try {
            pipeline.sh "rm -f ${scriptFile}"
        } catch (Exception e) {
            pipeline.logWarning("Failed to cleanup script file ${scriptFile}: ${e.message}")
        }

        try {
            if (credentialEnvFile && pipeline.fileExists(credentialEnvFile)) {
                pipeline.sh "shred -vfz -n 3 ${credentialEnvFile} 2>/dev/null || rm -f ${credentialEnvFile}"
                pipeline.logInfo('Credential environment file securely shredded')
            }
        } catch (Exception e) {
            pipeline.logWarning("Failed to cleanup credential environment file: ${e.message}")
        }
    }

    private List credentialsBindings() {
        return [
            pipeline.string(credentialsId: 'AWS_ACCESS_KEY_ID', variable: 'AWS_ACCESS_KEY_ID'),
            pipeline.string(credentialsId: 'AWS_SECRET_ACCESS_KEY', variable: 'AWS_SECRET_ACCESS_KEY'),
            pipeline.string(credentialsId: 'AWS_SSH_PEM_KEY', variable: 'AWS_SSH_PEM_KEY'),
            pipeline.string(credentialsId: 'AWS_SSH_KEY_NAME', variable: 'AWS_SSH_KEY_NAME'),
            pipeline.string(credentialsId: 'SLACK_WEBHOOK', variable: 'SLACK_WEBHOOK')
        ]
    }

    private String prepareFallbackDockerCommand(String scriptFile, Map extraEnv) {
        def directEnvVars = extractDirectEnvironmentVariables()
        def explicitEnvVars = prepareEnvironmentVariables(extraEnv)
        def inventoryMount = resolveInventoryMount()

        if (!pipeline.env.IMAGE_NAME) {
            pipeline.error 'ERROR: IMAGE_NAME environment variable is not set for fallback command'
        }

        def escapedContainerName = "${pipeline.env.BUILD_CONTAINER_NAME}-fallback".replaceAll('[^a-zA-Z0-9_-]', '_')
    def volumeMounts = collectFallbackVolumeMounts(scriptFile, inventoryMount)
    def volumeMountStr = volumeMounts.join(' \\\n+            ')

    def baseCmd = """docker run --rm \\\n+            ${volumeMountStr} \\\n+            --name ${escapedContainerName} \\\n+            -e QA_INFRA_WORK_PATH=/root/go/src/github.com/rancher/qa-infra-automation \\\n+            -e TF_WORKSPACE=\"${pipeline.env.TF_WORKSPACE}\" \\\n+            -e TERRAFORM_VARS_FILENAME=\"${pipeline.env.TERRAFORM_VARS_FILENAME}\" \\\n+            ${directEnvVars} \\\n+            ${explicitEnvVars} \\\n+            \"${pipeline.env.IMAGE_NAME.trim()}\"""

        def executionCmd = buildFallbackExecutionCommand()
        if (!pipeline.env.DEBUG?.toBoolean()) {
            executionCmd = """/bin/bash -c 'exec /bin/bash /tmp/script.sh'"""
        }

        return "${baseCmd} ${executionCmd}"
    }

    private List collectVolumeMounts(String envFilePath, String scriptFile, String inventoryMount) {
        def mounts = []

        if (pipeline.env.VALIDATION_VOLUME) {
            addVolumeIfExists(mounts, pipeline.env.VALIDATION_VOLUME, ':/root')
        } else {
            pipeline.logWarning('VALIDATION_VOLUME environment variable is not set')
        }

        def qaInfraPath = "${pipeline.pwd()}/qa-infra-automation"
        if (pipeline.fileExists(qaInfraPath)) {
            mounts.add("-v \"${qaInfraPath}:/root/go/src/github.com/rancher/qa-infra-automation\"")
        } else {
            pipeline.logWarning("QA infra automation path not found: ${qaInfraPath}")
        }

        if (pipeline.fileExists(scriptFile)) {
            mounts.add("-v \"${pipeline.pwd()}/${scriptFile}:/tmp/script.sh\"")
        }

        if (pipeline.fileExists(envFilePath)) {
            mounts.add("-v \"${envFilePath}:/tmp/.env\"")
        }

        if (pipeline.env.DEBUG?.toBoolean() && pipeline.fileExists('./tmp_debug_env_check.sh')) {
            mounts.add("-v \"${pipeline.pwd()}/tmp_debug_env_check.sh:/tmp/debug_env_check.sh\"")
        }

        if (inventoryMount) {
            mounts.add(inventoryMount.trim())
        }

        return mounts
    }

    private List collectFallbackVolumeMounts(String scriptFile, String inventoryMount) {
        def mounts = []

        if (pipeline.env.VALIDATION_VOLUME) {
            addVolumeIfExists(mounts, pipeline.env.VALIDATION_VOLUME, ':/root', ' (fallback)')
        } else {
            pipeline.logWarning('VALIDATION_VOLUME environment variable is not set (fallback)')
        }

        def qaInfraPath = "${pipeline.pwd()}/qa-infra-automation"
        if (pipeline.fileExists(qaInfraPath)) {
            mounts.add("-v \"${qaInfraPath}:/root/go/src/github.com/rancher/qa-infra-automation\"")
        } else {
            pipeline.logWarning("QA infra automation path not found in fallback: ${qaInfraPath}")
        }

        if (pipeline.fileExists(scriptFile)) {
            mounts.add("-v \"${pipeline.pwd()}/${scriptFile}:/tmp/script.sh\"")
        }

        if (pipeline.env.DEBUG?.toBoolean() && pipeline.fileExists('./tmp_debug_env_check.sh')) {
            mounts.add("-v \"${pipeline.pwd()}/tmp_debug_env_check.sh:/tmp/debug_env_check.sh\"")
        }

        if (inventoryMount) {
            mounts.add(inventoryMount.trim())
        }

        return mounts
    }

    private void addVolumeIfExists(List mounts, String volumeName, String targetPath, String logSuffix = '') {
        try {
            def status = pipeline.sh(script: "docker volume inspect ${volumeName} >/dev/null 2>&1", returnStatus: true)
            if (status == 0) {
                mounts.add("-v \"${volumeName}${targetPath}\"")
                pipeline.logInfo("Shared volume ${volumeName} found and will be mounted${logSuffix}")
            } else {
                pipeline.logWarning("Docker volume ${volumeName} does not exist${logSuffix}")
            }
        } catch (Exception e) {
            pipeline.logWarning("Failed to check Docker volume ${volumeName}${logSuffix}: ${e.message}")
        }
    }

    private void prepareDebugHelper() {
        try {
            if (pipeline.fileExists('validation/pipeline/scripts/debug_env_check.sh')) {
                pipeline.sh '''
                    cp validation/pipeline/scripts/debug_env_check.sh ./tmp_debug_env_check.sh || true
                    chmod +x ./tmp_debug_env_check.sh || true
                '''
                pipeline.logInfo('DEBUG helper copied to workspace: ./tmp_debug_env_check.sh')
            } else {
                pipeline.writeFile file: 'tmp_debug_env_check.sh', text: '''#!/bin/bash
set -e
echo "=== INLINE FALLBACK DEBUG SCRIPT ==="
echo "Printing /tmp/.env contents (masked):"
[ -f /tmp/.env ] && sed -n '1,200p' /tmp/.env || echo "/tmp/.env not found"
echo "Printing environment (selected keys):"
env | egrep 'AWS|S3|TF|RANCHER|RKE2|HOSTNAME' || env
echo "=== END DEBUG ==="
'''
                pipeline.sh 'chmod +x ./tmp_debug_env_check.sh || true'
                pipeline.logInfo('FALLBACK debug helper created at ./tmp_debug_env_check.sh')
            }
        } catch (Exception err) {
            pipeline.logWarning("Failed to prepare debug helper in workspace: ${err}")
        }
    }

    private String buildPrimaryExecutionCommand() {
        return '''/bin/bash -c '
        echo "=== DEBUG: Container started ==="
        echo "Current user: $(whoami)"
        echo "Current working directory: $(pwd)"
        echo "Environment file location: /tmp/.env"
        echo "Checking if /tmp/.env exists:"
        ls -la /tmp/.env || echo "FILE NOT FOUND"
        echo "Directory contents of /tmp:"
        ls -la /tmp/
        echo "=== END DEBUG ==="
        echo "Executing script: /tmp/script.sh"
        /bin/bash /tmp/debug_env_check.sh && exec /bin/bash /tmp/script.sh
    '''
    }

    private String buildFallbackExecutionCommand() {
        return '''/bin/bash -c '
        echo "=== DEBUG: Container started (FALLBACK MODE) ==="
        echo "Current user: $(whoami)"
        echo "Current working directory: $(pwd)"
        echo "Environment variables passed directly"
        echo "=== END DEBUG ==="
        echo "Executing script: /tmp/script.sh"
        /bin/bash /tmp/debug_env_check.sh && exec /bin/bash /tmp/script.sh
    '''
    }

    private String resolveInventoryMount() {
        def inventoryFile = 'ansible-inventory.yml'
        if (pipeline.fileExists(inventoryFile)) {
            pipeline.logInfo("Mounting inventory file from Jenkins workspace: ${inventoryFile}")
            return " -v ${pipeline.pwd()}/${inventoryFile}:/root/ansible/rke2/airgap/inventory.yml"
        }

        pipeline.logWarning("Inventory file not found in Jenkins workspace: ${inventoryFile}")
        pipeline.logInfo('Inventory file will be created in shared volume during infrastructure deployment')
        return ''
    }

    private void validateDockerEnvironment() {
        pipeline.logInfo('Validating Docker environment...')

        try {
            def dockerVersion = pipeline.sh(script: 'docker --version', returnStdout: true).trim()
            pipeline.logInfo("Docker version: ${dockerVersion}")

            def dockerInfo = pipeline.sh(script: 'docker info --format "Server Version: {{.ServerVersion}}"', returnStdout: true).trim()
            pipeline.logInfo("Docker server info: ${dockerInfo}")

            def diskSpace = pipeline.sh(script: 'df -h /var/lib/docker 2>/dev/null || df -h /', returnStdout: true).trim()
            pipeline.logInfo("Docker disk space: ${diskSpace}")

            validateDockerImage()
            pipeline.logInfo('[OK] Docker environment validation completed')
        } catch (Exception e) {
            pipeline.error("[FAIL] Docker environment validation failed: ${e.message}")
        }
    }

    private void validateDockerImage() {
        if (!pipeline.env.IMAGE_NAME) {
            return
        }

        try {
            def script = "docker image inspect ${pipeline.env.IMAGE_NAME} --format='{{.Id}}' 2>/dev/null || echo 'NOT_FOUND'"
            def imageExists = pipeline.sh(script: script, returnStdout: true).trim()
            if (imageExists == 'NOT_FOUND') {
                pipeline.logWarning("Docker image ${pipeline.env.IMAGE_NAME} not found locally - will be pulled during execution")
            } else {
                pipeline.logInfo("Docker image ${pipeline.env.IMAGE_NAME} is available locally")
            }
        } catch (Exception e) {
            pipeline.logWarning("Could not validate Docker image: ${e.message}")
        }
    }

    private void cleanupDanglingContainers() {
        pipeline.logInfo('Cleaning up any dangling containers...')

        try {
            def containerPattern = pipeline.env.BUILD_CONTAINER_NAME ?: 'build-container'

            pipeline.sh """
                docker ps -a --filter 'name=${containerPattern}' --format '{{.Names}}' | xargs -r docker rm -f 2>/dev/null || true

                docker container prune --force --filter 'until=1h' 2>/dev/null || true
            """

            pipeline.logInfo('[OK] Container cleanup completed')
        } catch (Exception e) {
            pipeline.logWarning("Container cleanup failed: ${e.message}")
        }
    }

    private String maskSensitiveData(String command) {
        def masked = command

        masked = masked.replaceAll(/-e "AWS_ACCESS_KEY_ID=[^"]+"/, '-e "AWS_ACCESS_KEY_ID=***"')
        masked = masked.replaceAll(/-e 'AWS_ACCESS_KEY_ID=[^']+'/, "-e 'AWS_ACCESS_KEY_ID=***'")
        masked = masked.replaceAll(/AWS_ACCESS_KEY_ID=[^\s]+/, 'AWS_ACCESS_KEY_ID=***')

        masked = masked.replaceAll(/-e "AWS_SECRET_ACCESS_KEY=[^"]+"/, '-e "AWS_SECRET_ACCESS_KEY=***"')
        masked = masked.replaceAll(/-e 'AWS_SECRET_ACCESS_KEY=[^']+'/, "-e 'AWS_SECRET_ACCESS_KEY=***'")
        masked = masked.replaceAll(/AWS_SECRET_ACCESS_KEY=[^\s]+/, 'AWS_SECRET_ACCESS_KEY=***')

        masked = masked.replaceAll(/-e "AWS_SSH_PEM_KEY=[^"]+"/, '-e "AWS_SSH_PEM_KEY=***"')
        masked = masked.replaceAll(/-e 'AWS_SSH_PEM_KEY=[^']+'/, "-e 'AWS_SSH_PEM_KEY=***'")
        masked = masked.replaceAll(/AWS_SSH_PEM_KEY=[^\s]+/, 'AWS_SSH_PEM_KEY=***')

        masked = masked.replaceAll(/-e "AWS_SSH_KEY_NAME=[^"]+"/, '-e "AWS_SSH_KEY_NAME=***"')
        masked = masked.replaceAll(/-e 'AWS_SSH_KEY_NAME=[^']+'/, "-e 'AWS_SSH_KEY_NAME=***'")
        masked = masked.replaceAll(/AWS_SSH_KEY_NAME=[^\s]+/, 'AWS_SSH_KEY_NAME=***')

        masked = masked.replaceAll(/-e "PRIVATE_REGISTRY_PASSWORD=[^"]+"/, '-e "PRIVATE_REGISTRY_PASSWORD=***"')
        masked = masked.replaceAll(/-e 'PRIVATE_REGISTRY_PASSWORD=[^']+'/, "-e 'PRIVATE_REGISTRY_PASSWORD=***'")
        masked = masked.replaceAll(/PRIVATE_REGISTRY_PASSWORD=[^\s]+/, 'PRIVATE_REGISTRY_PASSWORD=***')

        masked = masked.replaceAll(/-e "PRIVATE_REGISTRY_USERNAME=[^"]+"/, '-e "PRIVATE_REGISTRY_USERNAME=***"')
        masked = masked.replaceAll(/-e 'PRIVATE_REGISTRY_USERNAME=[^']+'/, "-e 'PRIVATE_REGISTRY_USERNAME=***'")
        masked = masked.replaceAll(/PRIVATE_REGISTRY_USERNAME=[^\s]+/, 'PRIVATE_REGISTRY_USERNAME=***')

        masked = masked.replaceAll(/-e "SLACK_WEBHOOK=[^"]+"/, '-e "SLACK_WEBHOOK=***"')
        masked = masked.replaceAll(/-e 'SLACK_WEBHOOK=[^']+'/, "-e 'SLACK_WEBHOOK=***'")
        masked = masked.replaceAll(/SLACK_WEBHOOK=[^\s]+/, 'SLACK_WEBHOOK=***')

        return masked
    }

    private void provideDockerDiagnostics() {
        pipeline.logInfo('=== DOCKER DIAGNOSTICS ===')

        try {
            pipeline.logInfo('Docker system information:')
            pipeline.sh 'docker system df 2>/dev/null || echo "Docker system info not available"'

            pipeline.logInfo('Docker container status:')
            def containerName = pipeline.env.BUILD_CONTAINER_NAME ?: 'build-container'
            pipeline.sh "docker ps -a --filter 'name=${containerName}' 2>/dev/null || echo 'No matching containers found'"

            pipeline.logInfo('Docker images:')
            def imageName = pipeline.env.IMAGE_NAME ?: 'latest'
            pipeline.sh "docker images | grep '${imageName}' 2>/dev/null || echo 'Target image not found'"

            pipeline.logInfo('System resources:')
            pipeline.sh 'free -h 2>/dev/null || echo "Memory info not available"'
            pipeline.sh 'df -h 2>/dev/null | head -5 || echo "Disk info not available"'
        } catch (Exception e) {
            pipeline.logWarning("Docker diagnostics failed: ${e.message}")
        }

        pipeline.logInfo('=== END DOCKER DIAGNOSTICS ===')
    }

    private String extractDirectEnvironmentVariables() {
        def envFile = pipeline.env.ENV_FILE
        if (!envFile || !pipeline.fileExists(envFile)) {
            return ''
        }

        def envFlags = new StringBuilder()

        try {
            def content = pipeline.readFile file: envFile
            content.split('\n').each { line ->
                def trimmed = line.trim()
                if (trimmed && !trimmed.startsWith('#') && trimmed.contains('=')) {
                    def parts = trimmed.split('=', 2)
                    if (parts.length == 2 && allowDirectEnvVariable(parts[0].trim())) {
                        def escaped = escapeForShell(parts[1].trim())
                        envFlags.append(" -e \"${parts[0].trim()}=${escaped}\"")
                    }
                }
            }
        } catch (Exception e) {
            pipeline.logWarning("Could not read environment file for direct variable passing: ${e.message}")
        }

        return envFlags.toString()
    }

    private boolean allowDirectEnvVariable(String key) {
        return key in [
            'AWS_REGION',
            'S3_BUCKET_NAME',
            'S3_REGION',
            'S3_KEY_PREFIX',
            'ANSIBLE_VARIABLES',
            'RKE2_VERSION',
            'RANCHER_VERSION',
            'HOSTNAME_PREFIX',
            'PRIVATE_REGISTRY_URL',
            'PRIVATE_REGISTRY_USERNAME',
            'RANCHER_HOSTNAME',
            'QA_INFRA_WORK_PATH',
            'TF_WORKSPACE',
            'TERRAFORM_VARS_FILENAME',
            'AWS_SSH_KEY_NAME'
        ]
    }

    private String writeCredentialEnvironmentFile() {
        def timestamp = System.currentTimeMillis()
        def credentialEnvFile = "docker-credentials-${timestamp}.env"
        def envContent = []

        appendIfPresent(envContent, 'AWS_ACCESS_KEY_ID', pipeline.env.AWS_ACCESS_KEY_ID)
        appendIfPresent(envContent, 'AWS_SECRET_ACCESS_KEY', pipeline.env.AWS_SECRET_ACCESS_KEY)
        appendIfPresent(envContent, 'AWS_SSH_PEM_KEY', pipeline.env.AWS_SSH_PEM_KEY)
        appendIfPresent(envContent, 'AWS_SSH_KEY_NAME', pipeline.env.AWS_SSH_KEY_NAME)
        appendIfPresent(envContent, 'SLACK_WEBHOOK', pipeline.env.SLACK_WEBHOOK)
        appendIfPresent(envContent, 'PRIVATE_REGISTRY_PASSWORD', pipeline.env.PRIVATE_REGISTRY_PASSWORD)

        pipeline.writeFile file: credentialEnvFile, text: envContent.join('\n')
        pipeline.sh "chmod 600 ${credentialEnvFile}"

        pipeline.logInfo("Created credential environment file: ${credentialEnvFile}")
        return credentialEnvFile
    }

    private void appendIfPresent(List envContent, String key, String value) {
        if (value) {
            envContent.add("${key}=${value}")
        }
    }

    private String addCredentialEnvFile(String dockerCmd, String credentialEnvFile) {
        if (!credentialEnvFile) {
            return dockerCmd
        }

        // Find a good insertion point after the --name <container> section so
        // we keep the env-file close to the container metadata options.
        def insertionPoint = dockerCmd.lastIndexOf('--name')
        if (insertionPoint == -1) {
            return dockerCmd
        }

        def nameEndIndex = dockerCmd.indexOf(' ', insertionPoint)
        if (nameEndIndex == -1) {
            return dockerCmd
        }

        def nextSpaceIndex = dockerCmd.indexOf(' ', nameEndIndex + 1)
        if (nextSpaceIndex == -1) {
            return dockerCmd
        }

    // Simpler approach: append the env-file flag at the end to avoid
    // fragile string insertion logic that can break quoting during load.
    return dockerCmd + " --env-file ${credentialEnvFile}"
    }

    private String escapeForShell(String value) {
        return value.replace('"', '\\"').replace('$', '\\$')
    }

}

def init(def pipeline) {
    return new DockerExecutionHelper(pipeline)
}

return this
