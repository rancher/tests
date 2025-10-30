// Phase 0 CI helpers (scaffold)

def readParamsYaml(ctx, String filePath = 'config/params.yaml') {
  try {
    if (ctx.fileExists(filePath)) {
      return ctx.readYaml(file: filePath) ?: [:]
    }
  } catch (ignored) {}
  return [:]
}

/**
 * Create a temporary credential env file containing sensitive credentials.
 * Returns the filename (relative to workspace).
 */
def createCredentialEnvironmentFile(ctx) {
  def timestamp = System.currentTimeMillis()
  def credentialEnvFile = "docker-credentials-${timestamp}.env"
  def envContent = []

  if (ctx.env.AWS_ACCESS_KEY_ID) {
    envContent.add("AWS_ACCESS_KEY_ID=${ctx.env.AWS_ACCESS_KEY_ID}")
  }
  if (ctx.env.AWS_SECRET_ACCESS_KEY) {
    envContent.add("AWS_SECRET_ACCESS_KEY=${ctx.env.AWS_SECRET_ACCESS_KEY}")
  }

  ctx.writeFile file: credentialEnvFile, text: envContent.join('\n')
  ctx.sh "chmod 600 ${credentialEnvFile}"
  ctx.echo "Created credential environment file: ${credentialEnvFile}"
  return credentialEnvFile
}

/**
 * Insert --env-file <file> into a docker run command string without exposing creds.
 * Assumes dockerCmd contains a --name <container> and then other args before image name.
 */
def addCredentialEnvFileToDockerCommand(dockerCmd, credentialEnvFile) {
  def modifiedCmd = dockerCmd
  if (credentialEnvFile) {
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
  }
  return modifiedCmd
}

/**
 * Execute a script content inside a docker container within the validation volume.
 * Uses withCredentials in calling Jenkinsfile to provide AWS creds into env; this helper
 * will still create a local credential env file and attach it to docker via --env-file.
 */
def executeScriptInContainer(ctx, scriptContent, extraEnv = [:], skipWorkspaceEnv = false) {
  def timestamp = System.currentTimeMillis()
  def containerName = "${ctx.env.BUILD_CONTAINER_NAME}-script-${timestamp}"
  def scriptFile = "docker-script-${timestamp}.sh"
  def credentialEnvFile = null

  ctx.writeFile file: scriptFile, text: scriptContent
  try {
    def envVars = ''
    extraEnv.each { k, v -> envVars += " -e ${k}=${v}" }
    // workspaceEnv intentionally unused; TF_WORKSPACE passed via --env-file or explicit -e where required

    // Create credential env file via helper
    credentialEnvFile = createCredentialEnvironmentFile(ctx)

    def dockerCmd = """
          docker run --rm \\
              -v ${ctx.env.VALIDATION_VOLUME}:/root \\
              -v ${ctx.pwd()}/${scriptFile}:/tmp/script.sh \\
              --name ${containerName} \\
              -t --env-file ${ctx.env.ENV_FILE} \\
              -e QA_INFRA_WORK_PATH=${ctx.env.QA_INFRA_WORK_PATH} \\
              -e TF_WORKSPACE=${ctx.env.TF_WORKSPACE} \\
              -e TERRAFORM_VARS_FILENAME=${ctx.env.TERRAFORM_VARS_FILENAME} \\
              ${envVars} \\
              ${ctx.env.IMAGE_NAME} \\
              sh /tmp/script.sh
      """

    def modified = addCredentialEnvFileToDockerCommand(dockerCmd, credentialEnvFile)
    ctx.sh modified
  } finally {
    ctx.sh "rm -f ${scriptFile} || true"
    if (credentialEnvFile && ctx.fileExists(credentialEnvFile)) {
      ctx.sh "shred -vfz -n 3 ${credentialEnvFile} 2>/dev/null || rm -f ${credentialEnvFile}"
      ctx.echo "Credential environment file securely shredded"
    }
  }
}

/**
 * Execute arbitrary shell commands inside a short-lived container (used for debug/collection)
 */
def executeInContainer(ctx, commands) {
  def commandString = commands.join(' && ')
  def timestamp = System.currentTimeMillis()
  def containerName = "${ctx.env.BUILD_CONTAINER_NAME}-${timestamp}"
  def scriptFile = "destroy-commands-${timestamp}.sh"
  def credentialEnvFile = null

  ctx.writeFile file: scriptFile, text: commandString
  try {
    credentialEnvFile = createCredentialEnvironmentFile(ctx)
    def dockerCmd = """
          docker run --rm \\
              -v ${ctx.env.VALIDATION_VOLUME}:/root \\
              -v ${ctx.pwd()}/${scriptFile}:/tmp/script.sh \\
              --name ${containerName} \\
              -t --env-file ${ctx.env.ENV_FILE} \\
              -e QA_INFRA_WORK_PATH=${ctx.env.QA_INFRA_WORK_PATH} \\
              -e TF_WORKSPACE=${ctx.env.TARGET_WORKSPACE} \\
              ${ctx.env.IMAGE_NAME} \\
              sh /tmp/script.sh
      """
    def modified = addCredentialEnvFileToDockerCommand(dockerCmd, credentialEnvFile)
    ctx.sh modified
  } finally {
    ctx.sh "rm -f ${scriptFile} || true"
    if (credentialEnvFile && ctx.fileExists(credentialEnvFile)) {
      ctx.sh "shred -vfz -n 3 ${credentialEnvFile} 2>/dev/null || rm -f ${credentialEnvFile}"
      ctx.echo "Credential environment file securely shredded"
    }
  }
}

/**
 * Lightweight docker/volume cleanup helper
 */
def cleanupContainersAndVolumes(ctx) {
  try {
    ctx.sh """
            docker ps -aq --filter "name=${ctx.env.BUILD_CONTAINER_NAME}" | xargs -r docker stop || true
            docker ps -aq --filter "name=${ctx.env.BUILD_CONTAINER_NAME}" | xargs -r docker rm -v || true
            docker rmi -f ${ctx.env.IMAGE_NAME} || true
            docker volume rm -f ${ctx.env.VALIDATION_VOLUME} || true
            docker system prune -f || true
        """
  } catch (ignored) {
    ctx.echo "Docker cleanup encountered issues: ${ignored.message}"
  }
}

/**
 * Build docker image helper
 */
def buildDockerImage(ctx) {
  ctx.echo "Building Docker image: ${ctx.env.IMAGE_NAME}"
  ctx.dir('./') {
    ctx.sh './tests/validation/configure.sh > /dev/null 2>&1 || true'
    def buildDate = ctx.sh(script: "date -u +'%Y-%m-%dT%H:%M:%SZ'", returnStdout: true).trim()
    def vcsRef = ctx.sh(script: 'git rev-parse --short HEAD 2>/dev/null || echo "unknown"', returnStdout: true).trim()
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
}

/**
 * Create a shared docker volume
 */
def createSharedVolume(ctx) {
  ctx.sh "docker volume create --name ${ctx.env.VALIDATION_VOLUME}"
}

return this
