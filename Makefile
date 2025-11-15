.PHONY: lint



# Minimal lint aggregator (best-effort)
lint:
	@set -e; \
	printf "Running shellcheck...\n"; \
	command -v shellcheck >/dev/null 2>&1 && shellcheck -S style scripts/**/*.sh || true; \
	printf "Running shfmt...\n"; \
	command -v shfmt >/dev/null 2>&1 && shfmt -d -i 2 -ci scripts || true; \
	printf "Running hadolint...\n"; \
	command -v hadolint >/dev/null 2>&1 && hadolint Dockerfile* || true; \
	printf "Lint complete.\n"
