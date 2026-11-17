#!/usr/bin/env bash
# Re-exec under bash if invoked via sh/dash (which lacks [[ and pipefail).
if [ -z "${BASH_VERSION:-}" ]; then
	exec bash "$0" "$@"
fi
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

sudo apt update
sudo apt install -y cloud-init iptables vim yq

echo "$username ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$username" >/dev/null
sudo chmod 440 "/etc/sudoers.d/$username"

sudo install -d -m 755 /etc/cloud/cloud.cfg.d
{
	printf 'allow_public_ssh_keys: true\n'
	printf 'system_info:\n  default_user:\n    name: "%s"\n' "$username"
} | sudo tee /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg >/dev/null
sudo chmod 644 /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg

# cloud-init.service was renamed to cloud-init-network.service in newer releases,
# so only enable the units that exist on this system.
for unit in cloud-init-local cloud-init cloud-init-network cloud-config cloud-final; do
	if systemctl cat "$unit.service" >/dev/null 2>&1; then
		sudo systemctl enable "$unit.service"
	fi
done

sudo cloud-init clean --logs
sudo rm -f /etc/ssh/ssh_host_*
sudo truncate -s 0 /etc/machine-id
