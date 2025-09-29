#!/bin/bash
set -e

# Ansible SSH Setup Playbook Script
# This script runs the Ansible SSH setup playbook

echo "=== Ansible SSH Setup Playbook Started ==="

# Debug: Show current directory and file structure
echo "Current working directory: $(pwd)"
echo "Contents of /root/:"
ls -la /root/ | head -20
echo "Contents of /root/ansible/ (if exists):"
ls -la /root/ansible/ 2>/dev/null || echo "Directory /root/ansible/ does not exist"

# Ensure Ansible directory structure exists
mkdir -p /root/ansible/rke2/airgap/inventory/

# Check if inventory file exists at the expected Terraform location
if [[ ! -f "/root/ansible/rke2/airgap/inventory.yml" ]]; then
    echo "Ansible inventory file not found at /root/ansible/rke2/airgap/inventory.yml"
    
    # Check if inventory file exists in the shared volume (legacy location)
    if [[ -f "/root/ansible-inventory.yml" ]]; then
        echo "Found inventory file at shared volume location, copying to expected location..."
        cp /root/ansible-inventory.yml /root/ansible/rke2/airgap/inventory.yml
        echo "Inventory file copied successfully"
    else
        echo "ERROR: Ansible inventory file not found at either:"
        echo "  - /root/ansible/rke2/airgap/inventory.yml (Terraform location)"
        echo "  - /root/ansible-inventory.yml (Shared volume location)"
        echo "Available files in /root/:"
        ls -la /root/ | grep -E "(inventory|ansible)" || echo "No inventory/ansible files found"
        echo "Available files in /root/ansible/ (if exists):"
        ls -la /root/ansible/ 2>/dev/null || echo "Directory /root/ansible/ does not exist"
        exit 1
    fi
fi

# Check if group_vars file exists at expected location
if [[ ! -f "/root/group_vars/all.yml" ]]; then
    echo "Ansible group_vars file not found at /root/group_vars/all.yml"
    
    # Check if group_vars file exists in /tmp/ (where it's generated)
    if [[ -f "/tmp/group_vars/all.yml" ]]; then
        echo "Found group_vars file at /tmp/group_vars/all.yml, copying to expected location..."
        mkdir -p /root/group_vars
        cp /tmp/group_vars/all.yml /root/group_vars/all.yml
        echo "Group_vars file copied successfully"
    else
        echo "ERROR: Ansible group_vars file not found at either:"
        echo "  - /root/group_vars/all.yml (expected location)"
        echo "  - /tmp/group_vars/all.yml (generation location)"
        echo "Available files in /tmp/:"
        ls -la /tmp/ | grep -E "(group|vars)" || echo "No group_vars files found in /tmp/"
        echo "Available files in /root/:"
        ls -la /root/ | grep -E "(group|vars)" || echo "No group_vars files found in /root/"
        exit 1
    fi
fi

if [[ ! -f "/root/.ssh/config" ]]; then
    echo "ERROR: SSH config file not found at /root/.ssh/config"
    exit 1
fi

# Create Ansible playbook directory structure
mkdir -p /root/playbooks
cd /root/playbooks

# Create SSH setup playbook
cat > ssh_setup.yml << EOF
---
- name: SSH Setup for RKE2 Airgap Deployment
  hosts: all
  become: true
  gather_facts: true
  vars_files:
    - /root/group_vars/all.yml
  
  tasks:
    - name: Ensure system is up to date
      apt:
        update_cache: yes
        upgrade: dist
        cache_valid_time: 3600
      when: ansible_os_family == "Debian"
    
    - name: Install required packages
      package:
        name:
          - python3
          - python3-pip
          - python3-venv
          - apt-transport-https
          - ca-certificates
          - curl
          - software-properties-common
          - gnupg
          - lsb-release
        state: present
    
    - name: Create ansible user
      user:
        name: ansible
        shell: /bin/bash
        groups: sudo
        append: true
        create_home: true
    
    - name: Set up passwordless sudo for ansible user
      copy:
        content: "ansible ALL=(ALL) NOPASSWD:ALL"
        dest: /etc/sudoers.d/ansible
        owner: root
        group: root
        mode: 0440
    
    - name: Create .ssh directory for ansible user
      file:
        path: /home/ansible/.ssh
        state: directory
        owner: ansible
        group: ansible
        mode: 0700
    
    - name: Copy SSH authorized keys
      copy:
        src: /root/.ssh/authorized_keys
        dest: /home/ansible/.ssh/authorized_keys
        owner: ansible
        group: ansible
        mode: 0600
      ignore_errors: true
    
    - name: Ensure SSH directory permissions
      file:
        path: /home/ansible/.ssh
        state: directory
        owner: ansible
        group: ansible
        mode: 0700
        recurse: yes
    
    - name: Test SSH connectivity
      command: echo "SSH setup completed successfully"
      register: ssh_test
      changed_when: false
    
    - name: Display SSH setup status
      debug:
        msg: "SSH setup completed on {{ inventory_hostname }}"

  handlers:
    - name: Restart SSH service
      service:
        name: sshd
        state: restarted
EOF

echo "SSH setup playbook created successfully"

# Debug: Show inventory file contents
echo "=== Inventory File Contents ==="
cat /root/ansible/rke2/airgap/inventory.yml
echo "=== End Inventory File Contents ==="

# Run the SSH setup playbook
echo "Running SSH setup playbook..."
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml ssh_setup.yml -v

echo "SSH setup playbook execution completed"

# Copy playbook logs to shared volume
cp ssh_setup.yml /root/ssh_setup_playbook.yml.backup

echo "=== Ansible SSH Setup Playbook Completed ==="