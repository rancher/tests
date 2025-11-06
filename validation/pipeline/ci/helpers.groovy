// CI helper utilities for Jenkins pipelines (Phase 0/1)

def readParamsYaml(ctx, String path) {
  def filePath = path ?: 'config/params.yaml'
  if (!ctx.fileExists(filePath)) {
    ctx.echo "[WARNING] params file not found: ${filePath}"
    return [:]
  }
  try {
    return ctx.readYaml(file: filePath) ?: [:]
  } catch (Exception e) {
    ctx.echo "[WARNING] Failed to read YAML ${filePath}: ${e.message}"
    return [:]
  }
}

return this
