#!/bin/bash

# Add Docker's official GPG key:
sudo apt update
sudo apt install ca-certificates curl make nginx
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

# Add the repository to Apt sources:
sudo tee /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Signed-By: /etc/apt/keyrings/docker.asc
EOF

sudo apt update

# harvester has a heavy dependency on v28 and below of docker for now. 
sudo apt install -y docker-ce=5:28.5.2-1~ubuntu.24.04~noble docker-ce-cli=5:28.5.2-1~ubuntu.24.04~noble containerd.io

sudo usermod -aG docker ubuntu

wget https://go.dev/dl/go1.25.5.linux-amd64.tar.gz

rm -rf /usr/local/go && tar -C /usr/local -xzf go1.25.5.linux-amd64.tar.gz

echo "export PATH=$PATH:/usr/local/go/bin" >> .bashrc

git clone https://github.com/harvester/harvester.git

cd harvester

git checkout v1.7
