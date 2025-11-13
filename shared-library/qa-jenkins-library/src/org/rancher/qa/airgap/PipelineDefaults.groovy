package org.rancher.qa.airgap

/**
 * Centralized defaults shared between setup and destroy airgap pipelines.
 */
class PipelineDefaults implements Serializable {

    private static final long serialVersionUID = 1L

    static final String DEFAULT_HOSTNAME_PREFIX = 'airgap-ansible-jenkins'
    static final String DEFAULT_RKE2_VERSION = 'v1.28.8+rke2r1'
    static final String DEFAULT_RANCHER_VERSION = 'v2.9-head'
    static final String DEFAULT_RANCHER_TEST_REPO = 'https://github.com/rancher/tests.git'
    static final String DEFAULT_QA_INFRA_REPO = 'https://github.com/rancher/qa-infra-automation.git'
    static final String DEFAULT_S3_BUCKET = 'rancher-terraform-state'
    static final String DEFAULT_S3_BUCKET_REGION = 'us-east-1'
    static final String CONTAINER_NAME_PREFIX = 'rancher-ansible-airgap'
    static final String SHARED_VOLUME_PREFIX = 'validation-volume'
    static final int TERRAFORM_TIMEOUT_MINUTES = 60
    static final int ANSIBLE_TIMEOUT_MINUTES = 90
    static final int VALIDATION_TIMEOUT_MINUTES = 30

    static final String LOG_PREFIX_INFO = '[INFO]'
    static final String LOG_PREFIX_ERROR = '[ERROR]'
    static final String LOG_PREFIX_WARNING = '[WARNING]'

}
