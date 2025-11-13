package org.rancher.qa.airgap

/**
 * Thin wrapper for running Terraform/Ansible helper scripts inside the airgap container.
 */
class InfrastructureManager implements Serializable {

    private static final long serialVersionUID = 1L

    private final DockerManager docker

    InfrastructureManager(DockerManager docker) {
        this.docker = docker
    }

    void deployInfrastructure(Map opts = [:]) {
        def script = '''#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_infrastructure_deploy.sh
'''
        docker.executeScriptInContainer([
            script: script,
            timeout: opts.timeout ?: PipelineDefaults.TERRAFORM_TIMEOUT_MINUTES,
            extraEnv: opts.extraEnv ?: [:]
        ])
    }

    void prepareAnsible(Map opts = [:]) {
        def script = '''#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/ansible_prepare_environment.sh
'''
        docker.executeScriptInContainer([
            script: script,
            timeout: opts.timeout ?: PipelineDefaults.ANSIBLE_TIMEOUT_MINUTES,
            extraEnv: opts.extraEnv ?: [:]
        ])
    }

    void deployRke2(Map opts = [:]) {
        def script = '''#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/ansible_deploy_rke2.sh
'''
        docker.executeScriptInContainer([
            script: script,
            timeout: opts.timeout ?: PipelineDefaults.ANSIBLE_TIMEOUT_MINUTES,
            extraEnv: opts.extraEnv ?: [:]
        ])
    }

    void deployRancher(Map opts = [:]) {
        def script = '''#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/ansible_deploy_rancher.sh
'''
        docker.executeScriptInContainer([
            script: script,
            timeout: opts.timeout ?: PipelineDefaults.ANSIBLE_TIMEOUT_MINUTES,
            extraEnv: opts.extraEnv ?: [:]
        ])
    }

    void destroyInfrastructure(Map opts = [:]) {
        def script = '''#!/bin/bash
set -e
source /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_destroy_infrastructure.sh
'''
        docker.executeScriptInContainer([
            script: script,
            timeout: opts.timeout ?: PipelineDefaults.TERRAFORM_TIMEOUT_MINUTES,
            extraEnv: opts.extraEnv ?: [:]
        ])
    }

}
