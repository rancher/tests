#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 5 ]]; then
	echo "Usage: $0 <username> <password> <ssh-public-key> <rhsm-org> <rhsm-activation-key>" >&2
	exit 2
fi

username=$1
password=$2
ssh_public_key=$3
rhsm_org=$4
rhsm_activation_key=$5

if [[ -z "$password" || -z "$ssh_public_key" || -z "$rhsm_org" || -z "$rhsm_activation_key" ]]; then
	echo "Password, SSH public key, RHSM org, and RHSM activation key must be non-empty." >&2
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
sudo restorecon -RF "$user_home/.ssh"

if ! sudo subscription-manager identity >/dev/null 2>&1; then
	sudo subscription-manager register --org "$rhsm_org" --activationkey "$rhsm_activation_key"
fi

sudo dnf install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-10.noarch.rpm

sudo dnf update -y
sudo dnf install -y cloud-init iptables kernel-modules-extra vim-enhanced yq curl docker helm kubectl


# Docker: enable the service and let the test user run docker without sudo.
sudo systemctl enable docker
sudo usermod -aG docker "$username"

sudo install -d -m 755 /etc/cloud/cloud.cfg.d
{
	printf 'allow_public_ssh_keys: true\n'
	printf 'system_info:\n  default_user:\n    name: "%s"\n' "$username"
} | sudo tee /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg >/dev/null
sudo chmod 644 /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg


sudo systemctl stop firewalld
sudo systemctl disable firewalld

# RHEL 10: load br_netfilter and nf_conntrack (from kernel-modules-extra) now and on every boot.
sudo modprobe br_netfilter
sudo modprobe nf_conntrack
printf 'br_netfilter\nnf_conntrack\n' | sudo tee /etc/modules-load.d/rancher-k8s.conf >/dev/null
sudo chmod 644 /etc/modules-load.d/rancher-k8s.conf

# Keep NetworkManager from managing CNI-created interfaces (RKE2 known issue).
sudo install -d -m 755 /etc/NetworkManager/conf.d
cat <<'EOF' | sudo tee /etc/NetworkManager/conf.d/rke2-canal.conf >/dev/null
[keyfile]
unmanaged-devices=interface-name:cali*;interface-name:flannel*;interface-name:cilium*;interface-name:lxc*
EOF
sudo chmod 644 /etc/NetworkManager/conf.d/rke2-canal.conf

echo "$username ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$username" >/dev/null
sudo chmod 440 "/etc/sudoers.d/$username"

sudo systemctl enable cloud-init.service
sudo systemctl enable cloud-config.service
sudo systemctl enable cloud-final.service
sudo systemctl enable cloud-init-local.service

sudo cloud-init clean --logs
sudo rm -f /etc/ssh/ssh_host_*
sudo truncate -s 0 /etc/machine-id

