#!/bin/bash
set -e

OUTPUT="${GITHUB_WORKSPACE}/cattle-config.yaml"
CA_CERTS=$(sed 's/^/    /' "${RANCHER_CA_FILE}")
cat > "$OUTPUT" <<EOF
rancher:
  host: "${RANCHER_HOST}"
  adminToken: "${RANCHER_ADMIN_TOKEN}"
  cleanup: true
  insecure: false
  clusterName: "${CLUSTER_NAME}"
  adminPassword: "${RANCHER_ADMIN_PASSWORD}"
  caFile: "${RANCHER_CA_FILE}"
  caCerts: |
${CA_CERTS}
  rancherCLI: false
EOF
