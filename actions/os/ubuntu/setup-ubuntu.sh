#!/usr/bin/env bash

set -euo pipefail

USERNAME=""
PASSWORD=""
SSH_PUBLIC_KEY=""
USER_HOME=""
USER_GROUP=""

ARCH=$(uname -m)
if [[ $ARCH == "x86_64" ]]; then
	ARCH="amd64"
elif [[ $ARCH == "arm64" || $ARCH == "aarch64" ]]; then
	ARCH="arm64"
fi

validate_args() {
	if [[ "$#" -ne 3 ]]; then
		usage
	fi

	USERNAME=$1
	PASSWORD=$2
	SSH_PUBLIC_KEY=$3

	if [[ -z "$PASSWORD" || -z "$SSH_PUBLIC_KEY" ]]; then
		echo "Password and SSH public key must be non-empty." >&2
		exit 2
	fi

	if [[ "${#PASSWORD}" -lt 8 ]]; then
		echo "Password must be at least 8 characters long." >&2
		exit 2
	fi

	if [[ ! "$USERNAME" =~ ^[a-z_][a-z0-9_-]*\$?$ ]]; then
		echo "Username must contain only lowercase letters, digits, underscores, and hyphens, and start with a letter or underscore." >&2
		exit 2
	fi
}

create_user() {
	if ! id "$USERNAME" >/dev/null 2>&1; then
		sudo useradd --create-home --shell /bin/bash "$USERNAME"
	fi

	printf '%s:%s\n' "$USERNAME" "$PASSWORD" | sudo chpasswd

	USER_HOME=$(getent passwd "$USERNAME" | cut -d: -f6)
	USER_GROUP=$(id -gn "$USERNAME")

	if [[ -z "$USER_HOME" || ! -d "$USER_HOME" ]]; then
		echo "Could not determine the home directory for $USERNAME." >&2
		exit 1
	fi
}

configure_ssh_key() {
	sudo install -d -m 700 -o "$USERNAME" -g "$USER_GROUP" "$USER_HOME/.ssh"
	printf '%s\n' "$SSH_PUBLIC_KEY" | sudo tee "$USER_HOME/.ssh/authorized_keys" >/dev/null
	sudo chown "$USERNAME:$USER_GROUP" "$USER_HOME/.ssh/authorized_keys"
	sudo chmod 600 "$USER_HOME/.ssh/authorized_keys"
}

install_packages() {
	sudo apt update
	sudo apt install -y cloud-init iptables vim yq curl jq wget

	install_helm
	install_docker
	install_kubectl
}

install_helm() {
	HELM_BUILDKITE_APT_KEY_ID="DDF78C3E6EBB2D2CC223C95C62BA89D07698DBC6"
	sudo apt-get install curl gpg --yes
	curl -fsSL https://packages.buildkite.com/helm-linux/helm-debian/gpgkey > "${TMPDIR:-/tmp}/helm.asc"

	if [ "$(gpg --import --import-options=show-only --with-colons "${TMPDIR:-/tmp}/helm.asc" \
			| awk -F: '$1 == "fpr" {print $10}' | head -n 1)" != "${HELM_BUILDKITE_APT_KEY_ID}" ]; then
		echo "ERROR: Unexpected Helm APT key ID: potential key compromise"
		exit 1
	fi

	sudo rm -f /etc/apt/sources.list.d/helm-stable-debian.list /usr/share/keyrings/helm.gpg
	sudo install -m 0644 -Dt /etc/apt/keyrings/ "${TMPDIR:-/tmp}/helm.asc"
	sudo tee /etc/apt/sources.list.d/helm-stable-debian.sources > /dev/null <<EOF
Types: deb
URIs: https://packages.buildkite.com/helm-linux/helm-debian/any/
Suites: any
Components: main
Signed-By: /etc/apt/keyrings/helm.asc
EOF

	sudo apt update
	sudo apt install -y helm
}

install_docker() {
	sudo apt update
	sudo apt install -y ca-certificates curl
	sudo install -m 0755 -d /etc/apt/keyrings
	sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
	sudo chmod a+r /etc/apt/keyrings/docker.asc

	sudo tee /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF

	sudo apt update
	sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

	sudo systemctl enable docker
	sudo systemctl start docker

	sudo usermod -aG docker "$USERNAME"
}

install_kubectl() {
	curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/$ARCH/kubectl"
	sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
	rm kubectl

	mkdir -p "$HOME/.kube"
}

configure_sudoers() {
	echo "$USERNAME ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$USERNAME" >/dev/null
	sudo chmod 440 "/etc/sudoers.d/$USERNAME"
}

configure_cloud_init() {
	sudo install -d -m 755 /etc/cloud/cloud.cfg.d
	{
		printf 'allow_public_ssh_keys: true\n'
		printf 'system_info:\n  default_user:\n    name: "%s"\n' "$USERNAME"
	} | sudo tee /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg >/dev/null
	sudo chmod 644 /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg
}

enable_cloud_init_services() {
	# cloud-init.service was renamed to cloud-init-network.service in newer releases,
	# so only enable the units that exist on this system.
	local unit
	for unit in cloud-init-local cloud-init cloud-init-network cloud-config cloud-final; do
		if systemctl cat "$unit.service" >/dev/null 2>&1; then
			sudo systemctl enable "$unit.service"
		fi
	done
}

cleanup_image() {
	sudo cloud-init clean --logs
	sudo rm -f /etc/ssh/ssh_host_*
	sudo truncate -s 0 /etc/machine-id
}

usage() {
	echo "Usage: $0 <username> <password> <ssh-public-key>" >&2
	exit 2
}

main() {
	validate_args "$@"
	create_user
	configure_ssh_key
	install_packages
	configure_sudoers
	configure_cloud_init
	enable_cloud_init_services
	cleanup_image
}

main "$@"