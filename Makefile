.PHONY: generate

# Phase 0: generate derived config artifacts from config/params.yaml
# Requires: yq (https://mikefarah.gitbook.io/yq/) and jq

generate:
	@set -e; \
	if ! command -v yq >/dev/null 2>&1; then echo "yq not found (install yq)" >&2; exit 1; fi; \
	mkdir -p terraform ansible/group_vars/all scripts; \
	yq -o=json '.' config/params.yaml > terraform/terraform.tfvars.json; \
	yq '.' config/params.yaml > ansible/group_vars/all/params.yaml; \
	yq 'to_entries | .[] | "\(.key)=\(.value)"' config/params.yaml > scripts/.env; \
	echo "Generated: terraform/terraform.tfvars.json, ansible/group_vars/all/params.yaml, scripts/.env"