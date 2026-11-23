#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 3 ]]; then
	echo "Usage: $0 <username> <password> <ssh-public-key>" >&2
	exit 2
fi

username=$1
password=$2
ssh_public_key=$3

if [[ -z "$password" || -z "$ssh_public_key" ]]; then
	echo "Password and SSH public key must be non-empty." >&2
	exit 2
fi

if [[ "${#password}" -lt 8 ]]; then
	echo "Password must be at least 8 characters long." >&2
	exit 2
fi

if [[ ! "$username" =~ ^[a-z_][a-z0-9_-]*\$?$ ]]; then
	echo "Username must contain only lowercase letters, digits, underscores, and hyphens, and start with a letter or underscore." >&2
	exit 2
fi

if ! id "$username" >/dev/null 2>&1; then
	sudo useradd --create-home --shell /bin/bash "$username"
fi

printf '%s:%s\n' "$username" "$password" | sudo chpasswd

user_home=$(getent passwd "$username" | cut -d: -f6)
user_group=$(id -gn "$username")
if [[ -z "$user_home" || ! -d "$user_home" ]]; then
	echo "Could not determine the home directory for $username." >&2
	exit 1
fi

sudo install -d -m 700 -o "$username" -g "$user_group" "$user_home/.ssh"
printf '%s\n' "$ssh_public_key" | sudo tee "$user_home/.ssh/authorized_keys" >/dev/null
sudo chown "$username:$user_group" "$user_home/.ssh/authorized_keys"
sudo chmod 600 "$user_home/.ssh/authorized_keys"

sudo dnf update -y
sudo dnf install -y cloud-init iptables grubby vim-enhanced yq curl docker

sudo systemctl disable nm-cloud-setup
sudo systemctl stop nm-cloud-setup
sudo systemctl disable nm-cloud-setup.timer
sudo systemctl stop nm-cloud-setup.timer

sudo systemctl stop firewalld
sudo systemctl disable firewalld

echo "$username ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$username" >/dev/null
sudo chmod 440 "/etc/sudoers.d/$username"


# Docker: enable the service and let the test user run docker without sudo.
sudo systemctl enable docker
sudo usermod -aG docker "$username"

# Install kubectl and helm from upstream release binaries.
case "$(uname -m)" in
	x86_64) arch=amd64 ;;
	aarch64) arch=arm64 ;;
	*)
		echo "Unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac
kubectl_version=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
curl -fsSL -o /tmp/kubectl "https://dl.k8s.io/release/${kubectl_version}/bin/linux/${arch}/kubectl"
sudo install -m 755 /tmp/kubectl /usr/local/bin/kubectl
rm -f /tmp/kubectl
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | sudo bash

sudo install -d -m 755 /etc/cloud/cloud.cfg.d
{
	printf 'allow_public_ssh_keys: true\n'
	printf 'system_info:\n  default_user:\n    name: "%s"\n' "$username"
} | sudo tee /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg >/dev/null
sudo chmod 644 /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg

sudo systemctl enable cloud-init.service
sudo systemctl enable cloud-config.service
sudo systemctl enable cloud-final.service
sudo systemctl enable cloud-init-local.service

sudo grubby --update-kernel=ALL --args="systemd.unified_cgroup_hierarchy=1"

sudo cloud-init clean --logs
sudo rm -f /etc/ssh/ssh_host_*
sudo truncate -s 0 /etc/machine-id

stat -fc %T /sys/fs/cgroup/
