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

if ! sudo subscription-manager identity >/dev/null 2>&1; then
	sudo subscription-manager register --org "$rhsm_org" --activationkey "$rhsm_activation_key"
fi

sudo dnf install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-9.noarch.rpm

sudo dnf update -y
sudo dnf install -y cloud-init iptables vim-enhanced yq

sudo systemctl disable nm-cloud-setup
sudo systemctl stop nm-cloud-setup
sudo systemctl disable nm-cloud-setup.timer
sudo systemctl stop nm-cloud-setup.timer

sudo systemctl stop firewalld
sudo systemctl disable firewalld

echo "$username ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$username" >/dev/null
sudo chmod 440 "/etc/sudoers.d/$username"

sudo systemctl enable cloud-init.service
sudo systemctl enable cloud-config.service
sudo systemctl enable cloud-final.service
sudo systemctl enable cloud-init-local.service

sudo cloud-init clean --logs
sudo rm -f /etc/ssh/ssh_host_*
sudo truncate -s 0 /etc/machine-id

