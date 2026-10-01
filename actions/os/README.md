# OS VM Setup Scripts

These scripts prepare a Linux VM for use as template rancher or downstream cluster nodes:

- `sles/sles.sh`
- `rhel/rhel9.sh`
- `oracle/oracle.sh`
- `ubuntu/ubuntu.sh`

## Common Setup

Each script accepts a username, password, and SSH public key. It creates the account if needed, sets its password, installs the public key in `authorized_keys` and sets up cloud init. 

## Distribution-Specific Setup

- **SLES** registers with SUSE using the supplied registration code and email, updates packages, and installs cloud-init and Vim. It stops and disables firewalld.
- **RHEL** updates packages and installs cloud-init, iptables, and Vim. It stops and disables NetworkManager cloud setup and firewalld.
- **Oracle Linux** follows the RHEL package and service setup, installs grubby, and adds `systemd.unified_cgroup_hierarchy=1` to all kernels. It reports the cgroup filesystem after cleanup.
- **Ubuntu** updates package indexes and installs cloud-init, iptables, and Vim. It expands `/dev/ubuntu-vg/ubuntu-lv` using available free space since that doesn't always occur by default.

## Run

To run the script you must be a sudoer and requires the following args::

```sh
sudo bash ./actions/os/rhel/rhel9.sh '<username>' '<password>' '<ssh-public-key>'
sudo bash ./actions/os/oracle/oracle.sh '<username>' '<password>' '<ssh-public-key>'
sudo bash ./actions/os/ubuntu/ubuntu.sh '<username>' '<password>' '<ssh-public-key>'
sudo bash ./actions/os/sles/sles.sh '<registration-code>' '<email>' '<username>' '<password>' '<ssh-public-key>'
```