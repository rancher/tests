#!/usr/bin/env bash

set -euo pipefail

REGISTRATION_CODE=""
EMAIL=""
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
	if [[ "$#" -ne 5 ]]; then
		usage
	fi

	REGISTRATION_CODE=$1
	EMAIL=$2
	USERNAME=$3
	PASSWORD=$4
	SSH_PUBLIC_KEY=$5

	if [[ -z "$REGISTRATION_CODE" || -z "$EMAIL" || -z "$PASSWORD" || -z "$SSH_PUBLIC_KEY" ]]; then
		echo "All five inputs must be non-empty." >&2
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

register_system() {
	SLES_VERSION=$(awk -F'"' '/^VERSION_ID=/ {print $2}' /etc/os-release)
	
	sudo SUSEConnect -r "$REGISTRATION_CODE" -e "$EMAIL"
	sudo SUSEConnect -p "SLES/$SLES_VERSION/$(uname -m)" -r "$REGISTRATION_CODE"
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

disable_firewalld() {
	sudo systemctl stop firewalld || true
	sudo systemctl disable firewalld || true
}

install_packages() {
	sudo zypper refresh
	sudo zypper update -y

	sudo zypper install -y cloud-init vim wget curl

	install_yq
	install_helm
	install_docker
	install_kubectl
}

install_yq() {
	sudo wget "https://github.com/mikefarah/yq/releases/latest/download/yq_linux_$ARCH" -O /usr/local/bin/yq && \
    sudo chmod +x /usr/local/bin/yq
}

install_helm() {
	curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4
	chmod 700 get_helm.sh
	./get_helm.sh

	rm get_helm.sh
}

install_docker() {
	sudo zypper install -y docker
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
	# Newer cloud-init enables its units via a generator, so some have no [Install] section and cannot be enabled.
	local unit state
	for unit in cloud-init-local cloud-init cloud-config cloud-final; do
		state=$(systemctl is-enabled "$unit.service" 2>/dev/null || true)
		case "$state" in
		enabled | static | generated | alias) ;;
		*) sudo systemctl enable "$unit.service" || echo "Warning: could not enable $unit.service (state: ${state:-unknown}); continuing." >&2 ;;
		esac
	done
}

cleanup_image() {
	sudo cloud-init clean --logs
	sudo rm -f /etc/ssh/ssh_host_*
	sudo truncate -s 0 /etc/machine-id
}

usage() {
	echo "Usage: $0 <registration-code> <email> <username> <password> <ssh-public-key>" >&2
	exit 2
}

main() {
	validate_args "$@"
	register_system
	create_user
	configure_ssh_key
	disable_firewalld
	install_packages
	configure_sudoers
	configure_cloud_init
	enable_cloud_init_services
	cleanup_image
}

main "$@"