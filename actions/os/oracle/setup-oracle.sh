#!/usr/bin/env bash

set -euo pipefail

USERNAME=""
PASSWORD=""
SSH_PUBLIC_KEY=""
USER_HOME=""
USER_GROUP=""

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
	sudo dnf update -y
	sudo dnf install -y cloud-init iptables grubby vim-enhanced yq curl wget jq
    
	install_helm
	install_docker
	install_kubectl
}

install_helm() {
	curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4
	chmod 700 get_helm.sh
	./get_helm.sh

	rm get_helm.sh
}

install_docker() {
	sudo dnf -y install dnf-plugins-core
	sudo dnf config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo
	sudo dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

	sudo systemctl enable --now docker
	sudo systemctl start docker
	sudo usermod -aG docker "$USERNAME"
}

install_kubectl() {
	curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/$ARCH/kubectl"
	sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
	rm kubectl

	mkdir -p "$HOME/.kube"
}

disable_nm_cloud_setup() {
	sudo systemctl disable nm-cloud-setup
	sudo systemctl stop nm-cloud-setup
	sudo systemctl disable nm-cloud-setup.timer
	sudo systemctl stop nm-cloud-setup.timer
}

disable_firewalld() {
	sudo systemctl stop firewalld
	sudo systemctl disable firewalld
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
	sudo systemctl enable cloud-init.service
	sudo systemctl enable cloud-config.service
	sudo systemctl enable cloud-final.service
	sudo systemctl enable cloud-init-local.service
}

configure_cgroups() {
	sudo grubby --update-kernel=ALL --args="systemd.unified_cgroup_hierarchy=1"
}

cleanup_image() {
	sudo cloud-init clean --logs
	sudo rm -f /etc/ssh/ssh_host_*
	sudo truncate -s 0 /etc/machine-id
}

main() {
	validate_args "$@"
	create_user
	configure_ssh_key
	install_packages
	disable_nm_cloud_setup
	disable_firewalld
	configure_sudoers
	configure_cloud_init
	enable_cloud_init_services
	configure_cgroups
	cleanup_image

	stat -fc %T /sys/fs/cgroup/
}

usage() {
	echo "Usage: $0 <USERNAME> <PASSWORD> <ssh-public-key>" >&2
	exit 2
}

main "$@"
