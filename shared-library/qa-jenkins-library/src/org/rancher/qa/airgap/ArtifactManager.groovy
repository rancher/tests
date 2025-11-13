package org.rancher.qa.airgap

/**
 * Handles artifact extraction and archiving tasks for the airgap pipelines.
 */
class ArtifactManager implements Serializable {

    private static final long serialVersionUID = 1L

    private final def steps

    ArtifactManager(def steps) {
        this.steps = steps
    }

    void extractFromVolume(String volumeName, String destination = 'artifacts') {
        if (!volumeName) {
            steps.error('VALIDATION_VOLUME is required for artifact extraction')
        }

        steps.echo "${PipelineDefaults.LOG_PREFIX_INFO} ${timestamp()} Extracting artifacts from ${volumeName}"
        steps.sh "mkdir -p ${destination}"

        def script = """
            docker run --rm \
                -v ${volumeName}:/source \
                -v ${steps.pwd()}/${destination}:/dest \
                alpine:latest \
                sh -c '
                    set -e
                    if [ -d "/source/artifacts" ]; then
                        cp -r /source/artifacts/* /dest/ || true
                    fi
                    if [ -f "/source/kubeconfig.yaml" ]; then
                        cp /source/kubeconfig.yaml /dest/
                    fi
                    if [ -f "/source/deployment-summary.json" ]; then
                        cp /source/deployment-summary.json /dest/
                    fi
                '
        """.stripIndent()
        steps.sh script
    }

    void archiveArtifacts(List patterns) {
        if (!patterns) {
            return
        }
        def joined = patterns.join(',')
        steps.archiveArtifacts artifacts: joined, allowEmptyArchive: true
        steps.echo "${PipelineDefaults.LOG_PREFIX_INFO} ${timestamp()} Archived artifacts: ${joined}"
    }

    private static String timestamp() {
        new Date().format('yyyy-MM-dd HH:mm:ss')
    }

}
