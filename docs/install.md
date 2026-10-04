# Install and upgrade

|  | Appliance (OVA) | Docker on Linux |
| --- | --- | --- |
| Runs on | vSphere 6.7 U2 or later | any Linux host with Docker Engine |
| Console | `ssh admin@appliance` from Windows, macOS or Linux | `rightsizer` on the Docker host |
| vCenter credentials | stored encrypted; collection resumes after a reboot once an admin logs in | kept in memory only; re-entered after a restart |
| Internet access | optional (updates only) | needed to pull the image |

## Appliance

1. Download `rightsizer-vX.Y.Z.ova` from the [latest release](https://github.com/MarcoColomb0/rightsizer/releases/latest).
2. In vCenter, choose **Deploy OVF Template** and select the file.
3. On **Customize template**, set:
   - **Administrator password:** at least 12 characters.
   - **Network:** hostname, IPv4 address with prefix (leave empty for DHCP), gateway, DNS, search domain and NTP.
   - **Preferences:** time zone, whether to check GitHub for rightsizer updates, and the **OS update channel**: `lts` (default; security fixes only, a restart about every three months) or `stable` (newer kernels and features, a restart about every month). You can move from `lts` to `stable` later, but not back.
4. Power on the VM. Its console shows the address and the SSH key fingerprint.
5. From any workstation, run `ssh admin@<appliance-address>`, check that the key fingerprint matches the one on the VM console, and enter the administrator password.

The appliance needs 2 vCPU, 2 GB of memory and about 14 GB of thin-provisioned disk. It needs HTTPS access to each vCenter. Workstations reach it on port 22 (console) and port 443 (report downloads). In vCenter, VMware Tools reports its guest OS as "rightsizer appliance" with the rightsizer and Flatcar versions.

Change the administrator password from the console (`c`) after the first login. Changing it later in the vApp options resets it and clears the stored vCenter credentials. That is the recovery path if the password is lost.

## Docker on Linux

Install [Docker Engine](https://docs.docker.com/engine/install/) for your distribution, then:

```bash
curl -fsSL https://github.com/MarcoColomb0/rightsizer/releases/latest/download/install.sh | sudo bash
rightsizer
```

The installer comes from the latest release, whose files cannot change once published. To check it before running it:

```bash
base=https://github.com/MarcoColomb0/rightsizer/releases/latest/download
curl -fsSLO "$base/install.sh" && curl -fsSLO "$base/SHA256SUMS"
grep '  install.sh$' SHA256SUMS | sha256sum -c -
gh attestation verify install.sh --repo MarcoColomb0/rightsizer   # optional: build provenance
sudo bash install.sh
```

| Installer option | Default | |
| --- | --- | --- |
| `--port` | `8443` | HTTPS port for report downloads |
| `--host` | primary IP | host name or IP used in download links |
| `--bind` | `0.0.0.0` | address the download port listens on |
| `--tag` | latest release | release to install, e.g. `v1.0.0` |
| `--build` | | build the image from source instead of pulling it |
| `--no-update-check` | | never look for new releases |

| Command | |
| --- | --- |
| `rightsizer` | open the console |
| `rightsizer status` | short status |
| `rightsizer export [file.pdf]` | copy the latest report to the current directory |
| `rightsizer backup` | back up collected data |
| `rightsizer update` | update to the latest release |
| `rightsizer logs` | show engine logs |
| `rightsizer uninstall` | remove rightsizer (collected data optional) |

## Upgrades

When a release is available, the console offers it when it opens, and `u` reopens the prompt. The prompt lists what changed in every version since the installed one, from [CHANGELOG.md](../CHANGELOG.md), with a link to the release page. Every upgrade protects your data:

1. download the new version while collection continues
2. stop the engine cleanly
3. back up the data (the last three backups are kept)
4. start the new version
5. check that it is healthy and has loaded every saved analysis
6. otherwise restore the backup and the previous version automatically

vCenter keeps one hour of real-time samples, so a short upgrade leaves no gap in the data.

On the appliance, the engine asks the host to perform the upgrade. The upgrade also updates the appliance itself: its systemd units, host helper, kernel and Docker settings travel inside the release and are applied with the same backup and rollback. Your session disconnects while the engine restarts; reconnect after about a minute.

The appliance asks for a restart only when one is needed: after an upgrade that changed kernel, Docker or console settings, or once Flatcar has downloaded an OS update (checked hourly). The console then shows **Restart required** with the reason, and `R` restarts it. Collection pauses after a restart until an administrator logs in again.

In the Docker install, the `rightsizer` launcher performs the upgrade and first offers an update for itself.

### Older appliances

Appliances deployed before v0.6.0 can upgrade their engine but not the appliance layer; deploy a newer OVA once, and later releases update everything. Appliances deployed before v0.8.0 run Flatcar stable and stay on it, because Flatcar never downgrades; deploy a newer OVA to use LTS.
