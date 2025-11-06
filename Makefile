.PHONY: generate lint

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

# Minimal lint aggregator (best-effort)
lint:
	@set -e; \
	printf "Running shellcheck...\n"; \
	command -v shellcheck >/dev/null 2>&1 && shellcheck -S style scripts/**/*.sh || true; \
	printf "Running shfmt...\n"; \
	command -v shfmt >/dev/null 2>&1 && shfmt -d -i 2 -ci scripts || true; \
	printf "Running terraform fmt...\n"; \
	command -v tofu >/dev/null 2>&1 && tofu fmt -recursive -check || true; \
	printf "Running tflint...\n"; \
	command -v tflint >/dev/null 2>&1 && tflint --recursive || true; \
	printf "Running ansible-lint...\n"; \
	command -v ansible-lint >/dev/null 2>&1 && ansible-lint -q || true; \
	printf "Running hadolint...\n"; \
	command -v hadolint >/dev/null 2>&1 && hadolint Dockerfile* || true; \
	printf "Lint complete.\n"