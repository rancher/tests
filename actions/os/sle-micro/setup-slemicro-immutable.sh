#!/usr/bin/env bash

set -euo pipefail

REGISTRATION_CODE=""
EMAIL=""
USERNAME=""
PASSWORD=""
SSH_PUBLIC_KEY=""
RUNTIME=""
RUNTIME_REPO=""
RUNTIME_REPO_PATH=""
SELINUX_PKG=""
USER_HOME=""
USER_GROUP=""

validate_args() {
	if [[ "$#" -ne 6 ]]; then
		usage
	fi

	REGISTRATION_CODE=$1
	EMAIL=$2
	USERNAME=$3
	PASSWORD=$4
	SSH_PUBLIC_KEY=$5
	RUNTIME=$6

	if [[ -z "$REGISTRATION_CODE" || -z "$EMAIL" || -z "$PASSWORD" || -z "$SSH_PUBLIC_KEY" ]]; then
		echo "Registration code, email, password, and SSH public key must be non-empty." >&2
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

	case "$RUNTIME" in
	k3s)
		RUNTIME_REPO=rancher-k3s-common
		RUNTIME_REPO_PATH=k3s
		SELINUX_PKG=k3s-selinux
		;;
	rke2)
		RUNTIME_REPO=rancher-rke2-common
		RUNTIME_REPO_PATH=rke2
		SELINUX_PKG=rke2-selinux
		;;
	*)
		echo "Runtime must be k3s or rke2, got: $RUNTIME" >&2
		exit 2
		;;
	esac
}

register_system() {
	# Immutable systems: registration is staged in a new snapshot and only active after a reboot.
	sudo transactional-update --non-interactive register -r "$REGISTRATION_CODE" -e "$EMAIL"
}

create_user() {
	# The group and user may already exist on a reused image, so make both creates safe to rerun.
	sudo groupadd -f "$USERNAME"
	if ! id "$USERNAME" >/dev/null 2>&1; then
		sudo useradd --create-home --shell /bin/bash -g "$USERNAME" "$USERNAME"
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
	# Repo, GPG key, refresh, and install share one snapshot so the install can see the Rancher repo.
	sudo transactional-update --continue --non-interactive run sh -c "
	  rpm --import https://rpm.rancher.io/public.key

	  cat > /etc/zypp/repos.d/${RUNTIME_REPO}.repo <<EOF
[${RUNTIME_REPO}-stable]
name=Rancher ${RUNTIME} Common (stable)
baseurl=https://rpm.rancher.io/${RUNTIME_REPO_PATH}/stable/common/slemicro/noarch
enabled=1
gpgcheck=1
repo_gpgcheck=0
gpgkey=https://rpm.rancher.io/public.key
EOF

	  zypper --non-interactive --gpg-auto-import-keys refresh
	  zypper --non-interactive install -y cloud-init curl docker ${SELINUX_PKG}
	"
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

print_post_reboot_steps() {
	echo "Packages are staged in a new snapshot. Reboot the VM to activate it."
	echo "After reboot, run: sudo usermod -aG docker $USERNAME && sudo systemctl enable docker"
	echo "Verify SELinux policy with: semodule -l | grep -E '$RUNTIME'"
}

usage() {
	echo "Usage: $0 <registration-code> <email> <username> <password> <ssh-public-key> <k3s|rke2>" >&2
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
	print_post_reboot_steps
}

main "$@"
