# Creating an Elemental Node on Harvester

This guide explains how to create a virtual machine (VM) on **Harvester** and install **Elemental** using a bootable ISO.

---

## 1. Upload the Elemental ISO
Upload the Elemental ISO image to Harvester so it can be used when creating the VM.

---

## 2. Create a Virtual Machine
When creating a new VM in Harvester, configure it with the following settings:

### **Basics**
- **CPU**: `4`
- **Memory**: `8 GiB`

### **Volumes**
1. **Boot Volume (ISO)**
   - **Type**: `cd-rom`
   - **Size**: `40 GiB`
   - **Image**: *Elemental ISO image*
   - **Bus**: `SATA`
2. **Disk Volume**
   - **Type**: `disk`
   - **Size**: `40 GiB`
   - **Storage Class**: `harvester-longhorn`
   - **Bus**: `VirtIO`

### **Networks**
- **Network**
  - **Name**: `default`
  - **Model**: `virtio`
  - **Network**: `Management Network`
  - **Type**: `masquerade`

### **Advanced Options**
- **OS Type**: `Linux`
- **Enable USB Tablet**: ✅
- **Install Guest Agent**: ✅
- **Enable TPM**: ✅
- **Boot in EFI mode**: ✅
- **Secure Boot**: ✅

---

## 3. Verify the Virtual Machine
Once created, the VM should appear in Harvester. Wait until its status is **Running**.

---

## 4. Check the Inventory
After the VM boots with Elemental, a new node will appear in the **Inventory**.  
Wait until the node’s status becomes **Active**.

---