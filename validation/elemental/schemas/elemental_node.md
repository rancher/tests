# Creating an Elemental Node on AWS EC2

This guide explains how to create a VM on an EC2 instance and install Elemental using a bootable ISO.  

## 1. Create an EC2 Instance
Launch an **EC2 instance** with the following configuration:
- **AMI**: Ubuntu
- **Architecture**: `x86_64`  
- **Boot mode**: `UEFI`  
- **Instance type**: `t2.xlarge`  
- **Storage**: `100 GiB`  
- **Network settings**:
  - Allow SSH (22)
  - Allow HTTP (80)
  - Allow HTTPS (443)
---

## 2. Install Required Packages
Connect to your EC2 instance via SSH and install `virt-manager`:

```bash
sudo apt-get update --yes
sudo apt-get install virt-manager --yes
```

## 3. Upload the Elemental ISO
From your local machine, upload the Elemental ISO to the /tmp folder of the EC2 instance:
```bash
scp -i /path/to/keypair.pem /path/to/elemental.iso ubuntu@<EC2_PUBLIC_IP>:/tmp/
```

## 4. Configure Environment Variables
On the EC2 instance, define the variables needed for VM creation:
```bash
export VM_NAME="vm-name"
export VM_ISO="/tmp/iso-name.iso"
export VM_NET="default"
export VM_OS="slem5.3"
export VM_IMG="${VM_NAME}.qcow2"
export VM_CORES=3
export VM_DISKSIZE=60
export VM_RAMSIZE=8000
```

## 5. Create the Virtual Machine
Run the following command to create the VM:
```bash
sudo virt-install \
--name ${VM_NAME} \
--memory ${VM_RAMSIZE} \
--vcpus ${VM_CORES} \
--os-variant=${VM_OS} \
--cdrom ${VM_ISO} \
--network network=${VM_NET},model=virtio \
--graphics vnc \
--disk path=/tmp/${VM_IMG},size=${VM_DISKSIZE},bus=virtio,format=qcow2 \
--boot uefi \
--cpu host-model \
--autoconsole text
```

## 6. Check the Inventory machine
A node should be created at the inventory machine, wait until it becomes Active