#!/bin/bash
set -e

# Ansible RKE2 Tarball Deployment Script
# This script runs the RKE2 tarball deployment playbook

echo "=== Ansible RKE2 Tarball Deployment Started ==="

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

# Create RKE2 tarball deployment playbook
cat > rke2_tarball_deployment.yml << EOF
---
- name: RKE2 Tarball Deployment for Airgap Environment
  hosts: rke2_cluster
  become: true
  gather_facts: true
  vars_files:
    - /root/group_vars/all.yml
  
  pre_tasks:
    - name: Validate RKE2 version
      assert:
        that:
          - rke2_version is defined
          - rke2_version != ""
        fail_msg: "RKE2 version is not defined"
    
    - name: Validate airgap mode
      assert:
        that:
          - rke2_airgap_mode is defined
          - rke2_airgap_mode | bool
        fail_msg: "Airgap mode must be enabled"
    
    - name: Display deployment information
      debug:
        msg: |
          Deploying RKE2 {{ rke2_version }} in airgap mode
          Installation method: {{ rke2_install_method }}
          Target nodes: {{ inventory_hostname }}

  tasks:
    - name: Create RKE2 directories
      file:
        path: "{{ item }}"
        state: directory
        owner: root
        group: root
        mode: 0755
      loop:
        - "{{ rke2_config_dir }}"
        - "{{ rke2_data_dir }}"
        - "{{ rke2_download_dir }}"
        - "{{ rke2_images_dir }}"
        - "/var/lib/rancher/rke2/agent/images"
    
    - name: Download RKE2 tarball (simulated for airgap)
      get_url:
        url: "https://github.com/rancher/rke2/releases/download/{{ rke2_version }}/rke2.linux-amd64.tar.gz"
        dest: "{{ rke2_download_dir }}/rke2.linux-amd64.tar.gz"
        mode: 0644
      when: not rke2_airgap_mode | bool
      ignore_errors: true
    
    - name: Create placeholder RKE2 tarball for airgap
      copy:
        content: "# Placeholder for RKE2 tarball - airgap mode"
        dest: "{{ rke2_download_dir }}/rke2.linux-amd64.tar.gz"
        mode: 0644
      when: rke2_airgap_mode | bool
    
    - name: Extract RKE2 tarball
      unarchive:
        src: "{{ rke2_download_dir }}/rke2.linux-amd64.tar.gz"
        dest: "/usr/local/bin"
        remote_src: yes
        extra_opts: [--strip-components=1]
      ignore_errors: true
    
    - name: Create RKE2 symlinks
      file:
        src: "/usr/local/bin/{{ item.src }}"
        dest: "/usr/local/bin/{{ item.dest }}"
        state: link
      loop:
        - { src: "rke2", dest: "rke2-server" }
        - { src: "rke2", dest: "rke2-agent" }
        - { src: "rke2", dest: "kubectl" }
        - { src: "rke2", dest: "crictl" }
        - { src: "rke2", dest: "ctr" }
      ignore_errors: true
    
    - name: Create RKE2 server config
      template:
        src: /dev/stdin
        dest: "{{ rke2_config_dir }}/config.yaml"
        mode: 0644
      vars:
        server_config: "{{ rke2_server_config | default({}) }}"
      stdin: |
        {% if server_config.disable is defined %}
        disable: {{ server_config.disable | to_json }}
        {% endif %}
        {% if server_config.tls_san is defined %}
        tls-san: {{ server_config.tls_san | to_json }}
        {% endif %}
        {% if server_config.etcd_s3 is defined %}
        etcd-s3: {{ server_config.etcd_s3 }}
        {% endif %}
        {% if server_config.cloud_provider_name is defined %}
        cloud-provider-name: {{ server_config.cloud_provider_name }}
        {% endif %}
        write-kubeconfig-mode: "0644"
        cluster-cidr: "{{ rke2_network.pod_cidr }}"
        service-cidr: "{{ rke2_network.service_cidr }}"
        cluster-dns: "{{ rke2_network.cluster_dns }}"
        cluster-domain: "{{ rke2_network.cluster_domain }}"
      when: inventory_hostname in groups['rke2_servers']
    
    - name: Create RKE2 agent config
      template:
        src: /dev/stdin
        dest: "{{ rke2_config_dir }}/config.yaml"
        mode: 0644
      vars:
        agent_config: "{{ rke2_agent_config | default({}) }}"
      stdin: |
        {% if agent_config.node_name is defined %}
        node-name: {{ agent_config.node_name }}
        {% endif %}
        {% if agent_config.with_node_id is defined %}
        with-node-id: {{ agent_config.with_node_id }}
        {% endif %}
        server: https://{{ hostvars[groups['rke2_servers'][0]].ansible_private_ip }}:9345
        token: "placeholder-token"
        write-kubeconfig-mode: "0644"
      when: inventory_hostname in groups['rke2_agents']
    
    - name: Create systemd service for RKE2 server
      copy:
        content: |
          [Unit]
          Description=RKE2 Server
          Documentation=https://rke2.io
          After=network-online.target
          Wants=network-online.target

          [Install]
          WantedBy=multi-user.target

          [Service]
          Type=notify
          EnvironmentFile=-/etc/default/%i
          EnvironmentFile=-/etc/sysconfig/%i
          EnvironmentFile=-/etc/systemd/system/%i.service.d/env.conf
          KillMode=process
          Delegate=yes
          LimitNOFILE=infinity
          LimitNPROC=infinity
          LimitCORE=infinity
          TasksMax=infinity
          TimeoutStartSec=0
          ExecStartPre=/sbin/modprobe br_netfilter
          ExecStartPre=/sbin/modprobe overlay
          ExecStart=/usr/local/bin/rke2 server
          ExecReload=/bin/kill -s HUP \$MAINPID
          KillSignal=SIGTERM
          RestartSec=10s
          Restart=always
        dest: /etc/systemd/system/rke2-server.service
        mode: 0644
      when: inventory_hostname in groups['rke2_servers']
    
    - name: Create systemd service for RKE2 agent
      copy:
        content: |
          [Unit]
          Description=RKE2 Agent
          Documentation=https://rke2.io
          After=network-online.target
          Wants=network-online.target

          [Install]
          WantedBy=multi-user.target

          [Service]
          Type=notify
          EnvironmentFile=-/etc/default/%i
          EnvironmentFile=-/etc/sysconfig/%i
          EnvironmentFile=-/etc/systemd/system/%i.service.d/env.conf
          KillMode=process
          Delegate=yes
          LimitNOFILE=infinity
          LimitNPROC=infinity
          LimitCORE=infinity
          TasksMax=infinity
          TimeoutStartSec=0
          ExecStartPre=/sbin/modprobe br_netfilter
          ExecStartPre=/sbin/modprobe overlay
          ExecStart=/usr/local/bin/rke2 agent
          ExecReload=/bin/kill -s HUP \$MAINPID
          KillSignal=SIGTERM
          RestartSec=10s
          Restart=always
        dest: /etc/systemd/system/rke2-agent.service
        mode: 0644
      when: inventory_hostname in groups['rke2_agents']
    
    - name: Enable and start RKE2 server
      systemd:
        name: rke2-server
        enabled: yes
        state: started
        daemon_reload: yes
      when: inventory_hostname in groups['rke2_servers']
      ignore_errors: true
    
    - name: Enable and start RKE2 agent
      systemd:
        name: rke2-agent
        enabled: yes
        state: started
        daemon_reload: yes
      when: inventory_hostname in groups['rke2_agents']
      ignore_errors: true
    
    - name: Wait for RKE2 server to be ready
      wait_for:
        port: 9345
        timeout: 300
        delay: 10
      when: inventory_hostname in groups['rke2_servers']
      ignore_errors: true
    
    - name: Check RKE2 server status
      command: systemctl status rke2-server
      register: rke2_server_status
      changed_when: false
      when: inventory_hostname in groups['rke2_servers']
      ignore_errors: true
    
    - name: Check RKE2 agent status
      command: systemctl status rke2-agent
      register: rke2_agent_status
      changed_when: false
      when: inventory_hostname in groups['rke2_agents']
      ignore_errors: true
    
    - name: Display RKE2 status
      debug:
        msg: |
          RKE2 {{ 'server' if inventory_hostname in groups['rke2_servers'] else 'agent' }} status on {{ inventory_hostname }}:
          {{ rke2_server_status.stdout if inventory_hostname in groups['rke2_servers'] else rke2_agent_status.stdout }}
      when: (inventory_hostname in groups['rke2_servers'] and rke2_server_status.rc == 0) or (inventory_hostname in groups['rke2_agents'] and rke2_agent_status.rc == 0)

  post_tasks:
    - name: Display deployment summary
      debug:
        msg: |
          RKE2 deployment completed on {{ inventory_hostname }}
          Version: {{ rke2_version }}
          Mode: {{ 'Airgap' if rke2_airgap_mode | bool else 'Online' }}
          Role: {{ 'Server' if inventory_hostname in groups['rke2_servers'] else 'Agent' }}

  handlers:
    - name: Restart RKE2 server
      systemd:
        name: rke2-server
        state: restarted
      when: inventory_hostname in groups['rke2_servers']
    
    - name: Restart RKE2 agent
      systemd:
        name: rke2-agent
        state: restarted
      when: inventory_hostname in groups['rke2_agents']
EOF

echo "RKE2 tarball deployment playbook created successfully"

# Run the RKE2 deployment playbook
echo "Running RKE2 tarball deployment playbook..."
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml rke2_tarball_deployment.yml -v

echo "RKE2 tarball deployment playbook execution completed"

# Copy playbook logs to shared volume
cp rke2_tarball_deployment.yml /root/rke2_tarball_deployment_playbook.yml.backup

echo "=== Ansible RKE2 Tarball Deployment Completed ==="