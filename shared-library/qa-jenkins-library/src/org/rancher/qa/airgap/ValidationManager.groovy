import hudson.AbortException

package org.rancher.qa.airgap

/**
 * Provides access to validation helper scripts that live in the rancher/tests repository.
 */
class ValidationManager implements Serializable {

    private static final long serialVersionUID = 1L

    private final def steps

    ValidationManager(def steps) {
        this.steps = steps
    }

    void validatePipelineParameters(Map ctx) {
        def envVars = [
            'RKE2_VERSION'        : valueFor(ctx, 'RKE2_VERSION'),
            'RANCHER_VERSION'     : valueFor(ctx, 'RANCHER_VERSION'),
            'RANCHER_TEST_REPO_URL': valueFor(ctx, 'RANCHER_TEST_REPO_URL'),
            'QA_INFRA_REPO_URL'   : valueFor(ctx, 'QA_INFRA_REPO_URL')
        ]
        runHelper(
            'Validate pipeline parameters',
            envVars,
            'validate_pipeline_parameters'
        )
    }

    void ensureRequiredVariables(Map ctx, List<String> requiredVars) {
        if (!requiredVars) {
            return
        }
        def envVars = requiredVars.collectEntries { name ->
            [(name): valueFor(ctx, name)]
        }
        envVars['ENV_FILE'] = valueFor(ctx, 'ENV_FILE')
        envVars['SSH_KEY_PATH'] = valueFor(ctx, 'SSH_KEY_PATH')

        def args = requiredVars.collect { "\"${it}\"" }.join(' ')
        def invocation = args ? "validate_required_variables ${args}" : 'validate_required_variables'

        runHelper(
            'Validate required environment variables',
            envVars,
            invocation
        )
    }

    void validateSensitiveDataHandling(Map ctx, boolean required = false) {
        def envVars = [
            'ENV_FILE'    : valueFor(ctx, 'ENV_FILE'),
            'SSH_KEY_PATH': valueFor(ctx, 'SSH_KEY_PATH')
        ]
        runHelper(
            'Validate sensitive data handling',
            envVars,
            'validate_sensitive_data_handling "$ENV_FILE"',
            required
        )
    }

    private boolean runHelper(String label, Map<String, String> envVars, String invocation, boolean required = true) {
        def helperPath = resolveWorkspacePath([
            'validation/pipeline/scripts/airgap/validation_helpers.sh',
            'tests/validation/pipeline/scripts/airgap/validation_helpers.sh'
        ])

        if (!helperPath) {
            def message = 'validation_helpers.sh not found; skipping requested validation'
            if (required) {
                steps.error(message)
            } else {
                steps.echo("${PipelineDefaults.LOG_PREFIX_WARNING} ${timestamp()} ${message}")
            }
            return false
        }

        def workspaceRoot = steps.pwd()
        def helperAbsolute = toWorkspaceAbsolute(helperPath, workspaceRoot)
        def envList = new ArrayList<String>()
        envVars?.each { key, value ->
            envList << "${key}=${value ?: ''}"
        }
        envList << "VALIDATION_HELPER_PATH=${helperAbsolute}"
        envList << "WORKSPACE_ROOT=${workspaceRoot}"

        def commonPath = resolveWorkspacePath([
            'scripts/lib/common.sh',
            'validation/pipeline/scripts/lib/common.sh',
            'tests/scripts/lib/common.sh',
            'tests/validation/pipeline/scripts/lib/common.sh'
        ], false)
        if (commonPath) {
            envList << "COMMON_HELPER_PATH=${toWorkspaceAbsolute(commonPath, workspaceRoot)}"
        }

        def scriptLines = [
            '#!/bin/bash',
            'set -Eeuo pipefail',
            'source "$VALIDATION_HELPER_PATH"'
        ]
        if (invocation?.trim()) {
            scriptLines << invocation
        }

        steps.withEnv(envList) {
            steps.sh(label: label, script: scriptLines.join('\n') + '\n')
        }
        return true
    }

    private String resolveWorkspacePath(List<String> candidates, boolean required = true) {
        if (!candidates) {
            return null
        }
        for (candidate in candidates) {
            try {
                if (steps.fileExists(candidate)) {
                    return candidate
                }
            } catch (groovy.lang.MissingMethodException | AbortException ignored) {
                // fileExists may not be available outside node context; continue
            }
        }
        if (required) {
            steps.echo(
                "${PipelineDefaults.LOG_PREFIX_WARNING} ${timestamp()} " +
                'Required workspace file not found. Checked: ' + candidates
            )
        }
        return null
    }

    private static String toWorkspaceAbsolute(String path, String workspaceRoot) {
        if (!path) {
            return null
        }
        if (path.startsWith('/') || path.startsWith('\\') || path ==~ /^[A-Za-z]:[\\/].*/) {
            return path
        }
        if (workspaceRoot?.trim()) {
            return workspaceRoot.endsWith('/') ? "${workspaceRoot}${path}" : "${workspaceRoot}/${path}"
        }
        return path
    }

    private String valueFor(Map ctx, String key) {
        def fromCtx = ctx?.get(key)
        if (fromCtx != null) {
            return fromCtx.toString()
        }
        def fromEnv = steps.env."${key}"
        return fromEnv ?: ''
    }

    private static String timestamp() {
        new Date().format('yyyy-MM-dd HH:mm:ss')
    }
}
