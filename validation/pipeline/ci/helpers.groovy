// Phase 0 CI helpers (scaffold)

def readParamsYaml(ctx, String filePath = 'config/params.yaml') {
  try {
    if (ctx.fileExists(filePath)) {
      return ctx.readYaml(file: filePath) ?: [:]
    }
  } catch (ignored) {}
  return [:]
}

return this
