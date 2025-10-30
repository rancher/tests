// Airgap pipeline step library (Phase 2)

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

return this
