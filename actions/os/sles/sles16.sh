#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 5 ]]; then
	echo "Usage: $0 <registration-code> <email> <username> <password> <ssh-public-key>" >&2
	exit 2
fi

registration_code=$1
email=$2
username=$3
password=$4
ssh_public_key=$5

if [[ -z "$registration_code" || -z "$email" || -z "$password" || -z "$ssh_public_key" ]]; then
	echo "All five inputs must be non-empty." >&2
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

sudo SUSEConnect -r "$registration_code" -e "$email"
sudo SUSEConnect -p "SLES/16.0/$(uname -m)" -r "$registration_code"

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

sudo systemctl stop firewalld || true
sudo systemctl disable firewalld || true

sudo zypper refresh
sudo zypper update

echo "$username ALL=(ALL) NOPASSWD:ALL" | sudo tee "/etc/sudoers.d/$username" >/dev/null
sudo chmod 440 "/etc/sudoers.d/$username"

sudo zypper install -y cloud-init vim

sudo zypper install -y yq

sudo install -d -m 755 /etc/cloud/cloud.cfg.d
{
	printf 'allow_public_ssh_keys: true\n'
	printf 'system_info:\n  default_user:\n    name: "%s"\n' "$username"
} | sudo tee /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg >/dev/null
sudo chmod 644 /etc/cloud/cloud.cfg.d/99-rancher-ssh-user.cfg

# Newer cloud-init enables its units via a generator, so some have no [Install] section and cannot be enabled.
for unit in cloud-init-local cloud-init cloud-config cloud-final; do
	state=$(systemctl is-enabled "$unit.service" 2>/dev/null || true)
	case "$state" in
	enabled | static | generated | alias) ;;
	*) sudo systemctl enable "$unit.service" || echo "Warning: could not enable $unit.service (state: ${state:-unknown}); continuing." >&2 ;;
	esac
done

sudo cloud-init clean --logs
sudo rm -f /etc/ssh/ssh_host_*
sudo truncate -s 0 /etc/machine-id