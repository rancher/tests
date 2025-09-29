#!/bin/bash
set -e

# Ansible Kubectl Access Setup Script
# This script sets up kubectl access on the bastion host

echo "=== Ansible Kubectl Access Setup Started ==="

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

if [[ ! -f "/root/group_vars/all.yml" ]]; then
    echo "ERROR: Ansible group_vars file not found at /root/group_vars/all.yml"
    exit 1
fi

# Create Ansible playbook directory structure
mkdir -p /root/playbooks
cd /root/playbooks

# Create kubectl access setup playbook
cat > kubectl_access_setup.yml << EOF
---
- name: Kubectl Access Setup on Bastion
  hosts: bastion
  become: true
  gather_facts: true
  vars_files:
    - /root/group_vars/all.yml
  
  pre_tasks:
    - name: Display kubectl setup information
      debug:
        msg: |
          Setting up kubectl access on bastion
          RKE2 version: {{ rke2_version }}
          Config directory: {{ kube_config_dir }}

  tasks:
    - name: Create kubectl configuration directory
      file:
        path: "{{ kube_config_dir }}"
        state: directory
        owner: root
        group: root
        mode: 0755
    
    - name: Copy RKE2 kubeconfig from first server
      fetch:
        src: /etc/rancher/rke2/rke2.yaml
        dest: "{{ kube_config_dir }}/config"
        flat: yes
      delegate_to: "{{ groups['rke2_servers'][0] }}"
      when: groups['rke2_servers'] is defined and groups['rke2_servers'] | length > 0
      ignore_errors: true
    
    - name: Update kubeconfig server address
      replace:
        path: "{{ kube_config_dir }}/config"
        regexp: 'server: https://127.0.0.1:6443'
        replace: 'server: https://{{ hostvars[groups['rke2_servers'][0]].ansible_private_ip }}:6443'
      when: groups['rke2_servers'] is defined and groups['rke2_servers'] | length > 0
      ignore_errors: true
    
    - name: Set kubeconfig permissions
      file:
        path: "{{ kube_config_dir }}/config"
        owner: root
        group: root
        mode: 0644
      ignore_errors: true
    
    - name: Create kubectl symlink
      file:
        src: /usr/local/bin/kubectl
        dest: /usr/local/bin/kubectl
        state: link
      ignore_errors: true
    
    - name: Test kubectl connectivity
      command: kubectl cluster-info
      register: kubectl_test
      changed_when: false
      ignore_errors: true
    
    - name: Display kubectl test results
      debug:
        msg: |
          Kubectl connectivity test results:
          {{ kubectl_test.stdout }}
          {{ kubectl_test.stderr }}
      when: kubectl_test.rc == 0
    
    - name: Get cluster nodes
      command: kubectl get nodes
      register: cluster_nodes
      changed_when: false
      ignore_errors: true
    
    - name: Display cluster nodes
      debug:
        msg: |
          Cluster nodes:
          {{ cluster_nodes.stdout }}
      when: cluster_nodes.rc == 0
    
    - name: Get cluster pods
      command: kubectl get pods -A
      register: cluster_pods
      changed_when: false
      ignore_errors: true
    
    - name: Display cluster pods
      debug:
        msg: |
          Cluster pods:
          {{ cluster_pods.stdout }}
      when: cluster_pods.rc == 0
    
    - name: Create kubectl aliases
      copy:
        content: |
          # Kubectl aliases
          alias k='kubectl'
          alias kgp='kubectl get pods'
          alias kgn='kubectl get nodes'
          alias kga='kubectl get all'
          alias kaf='kubectl apply -f'
          alias kdf='kubectl delete -f'
          alias kl='kubectl logs'
          alias ke='kubectl exec -it'
        dest: /etc/profile.d/kubectl-aliases.sh
        mode: 0644
    
    - name: Create kubectl completion
      command: kubectl completion bash > /etc/bash_completion.d/kubectl
      args:
        creates: /etc/bash_completion.d/kubectl
      ignore_errors: true
    
    - name: Create kubectl configuration summary
      copy:
        content: |
          # Kubectl Configuration Summary
          # Generated on: {{ ansible_date_time.iso8601 }}
          # Bastion: {{ inventory_hostname }}
          
          Kubeconfig location: {{ kube_config_dir }}/config
          Kubectl binary: /usr/local/bin/kubectl
          
          Cluster nodes:
          {% if cluster_nodes.rc == 0 %}
          {{ cluster_nodes.stdout }}
          {% else %}
          Unable to retrieve cluster nodes
          {% endif %}
          
          Cluster pods:
          {% if cluster_pods.rc == 0 %}
          {{ cluster_pods.stdout }}
          {% else %}
          Unable to retrieve cluster pods
          {% endif %}
          
          Kubectl aliases available:
          - k (kubectl)
          - kgp (kubectl get pods)
          - kgn (kubectl get nodes)
          - kga (kubectl get all)
          - kaf (kubectl apply -f)
          - kdf (kubectl delete -f)
          - kl (kubectl logs)
          - ke (kubectl exec -it)
        dest: /root/kubectl-setup-summary.txt
        mode: 0644

  post_tasks:
    - name: Display kubectl setup completion
      debug:
        msg: |
          Kubectl access setup completed on {{ inventory_hostname }}
          Kubeconfig: {{ kube_config_dir }}/config
          Test command: kubectl cluster-info

  handlers:
    - name: Reload bash completion
      command: source /etc/bash_completion.d/kubectl
      ignore_errors: true
EOF

echo "Kubectl access setup playbook created successfully"

# Run the kubectl access setup playbook
echo "Running kubectl access setup playbook..."
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml kubectl_access_setup.yml -v

echo "Kubectl access setup playbook execution completed"

# Copy playbook logs to shared volume
cp kubectl_access_setup.yml /root/kubectl_access_setup_playbook.yml.backup

echo "=== Ansible Kubectl Access Setup Completed ==="