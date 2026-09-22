# rightsizer

**Size your next hardware refresh on what your VMs use, not on what they were given.**

rightsizer watches one or more VMware vCenters for 24 hours to 14 days, compares each VM's provisioned vCPU, memory and storage with its measured use, and tells you what the environment actually needs. It never changes anything in vCenter.

[![ci](https://github.com/MarcoColomb0/rightsizer/actions/workflows/ci.yml/badge.svg)](https://github.com/MarcoColomb0/rightsizer/actions/workflows/ci.yml)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

## Features

- **VM rightsizing:** oversized and undersized vCPU and memory, with a confidence level for each recommendation.
- **Refresh sizing:** required GHz, cores, RAM and hosts per cluster, including an HA spare. The figures are hardware-neutral, so you can apply them to any server model.
- **Waste:** idle VMs, VMs powered off for the whole window, old snapshots, and thick disks that are mostly empty.
- **Several vCenters:** analyse many at once, with a report per vCenter or one combined report.
- **Live console:** a terminal UI shows progress and findings while data is collected.
- **PDF report:** executive summary, per-cluster charts, prioritised findings and methodology.

## Deploy

Pick one of the two options.

|  | Appliance (OVA) | Docker on Linux |
| --- | --- | --- |
| Runs on | vSphere 6.7 U2 or later | any Linux host with Docker Engine |
| Console | `ssh admin@appliance` from Windows, macOS or Linux | `rightsizer` on the Docker host |
| vCenter credentials | stored encrypted, collection resumes after a reboot once an admin logs in | kept in memory only, re-entered after a restart |
| Internet access | optional (updates only) | needed to pull the image |

### Option A: appliance

1. Download `rightsizer-vX.Y.Z.ova` from the [latest release](https://github.com/MarcoColomb0/rightsizer/releases/latest).
2. In vCenter, choose **Deploy OVF Template** and select the file.
3. On **Customize template**, set:
   - **Administrator password:** at least 12 characters.
   - **Network:** hostname, IPv4 address with prefix (leave empty for DHCP), gateway, DNS, search domain and NTP.
   - **Preferences:** time zone, and whether to check GitHub for updates.
4. Power on the VM. Its console shows the address and the SSH key fingerprint.
5. From any workstation, run:

   ```bash
   ssh admin@<appliance-address>
   ```

   Check that the key fingerprint matches the one on the VM console, then enter the administrator password.

The appliance needs 2 vCPU, 2 GB of memory and about 14 GB of thin-provisioned disk. It needs HTTPS access to each vCenter. Workstations reach it on port 22 (console) and port 443 (report downloads).

Change the administrator password from the console (`c`) after the first login. Changing it later in the vApp options resets it and clears the stored vCenter credentials. That is the recovery path if the password is lost.

### Option B: Docker on Linux

Requirements: [Docker Engine](https://docs.docker.com/engine/install/). Follow the guide for your distribution: [Ubuntu](https://docs.docker.com/engine/install/ubuntu/), [Debian](https://docs.docker.com/engine/install/debian/), [RHEL](https://docs.docker.com/engine/install/rhel/), [Fedora](https://docs.docker.com/engine/install/fedora/) or [others](https://docs.docker.com/engine/install/#supported-platforms).

```bash
curl -fsSL https://raw.githubusercontent.com/MarcoColomb0/rightsizer/main/install.sh | sudo bash
rightsizer
```

| Option | Default | |
| --- | --- | --- |
| `--port` | `8443` | HTTPS port for report downloads |
| `--host` | primary IP | host name or IP used in download links |
| `--bind` | `0.0.0.0` | address the download port listens on |
| `--tag` | latest release | release to install, e.g. `v0.2.0` |
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

## Using the console

1. Press `a` to add a vCenter. Enter its address, a user with the built-in **Read-only** role, the duration and a sizing profile.
2. Compare the certificate fingerprint with the one shown in vCenter, then press `y`.
3. Repeat for other vCenters, then leave with `q`. Collection continues in the background.

On the home screen, `enter` opens a vCenter, `p` builds a combined PDF, and `s` stops sharing reports. Inside a vCenter, `p` builds its PDF, `f` finishes early, `r` resumes a paused analysis, and `x` removes it with its data.

A PDF is offered for download on a temporary HTTPS link shown in the console. When an analysis ends, its final report is shared automatically.

## Security

- **Read-only by construction.** Every vSphere call passes an allowlist of read methods, and anything else is blocked before it leaves the process. A test checks that power and delete operations are refused.
- **Certificate pinning.** Self-signed vCenter certificates are accepted only after you confirm their SHA-256 fingerprint. After that, any other certificate is refused.
- **Credentials.** On the appliance, vCenter passwords are encrypted with XChaCha20-Poly1305. The key is derived from the administrator password with Argon2id, and it exists only in memory after an administrator logs in. In the Docker install, passwords are never written to disk. Collection pauses after three failed vCenter logins, so a changed password can't lock the account.
- **SSH console.** Only the `admin` user can log in, only with the administrator password. A terminal is required, and commands, sftp, agent and port forwarding are refused. Only modern key exchanges and ciphers are offered. An address is locked out after five failed attempts in 15 minutes.
- **Appliance host.** Built on [Flatcar Container Linux](https://www.flatcar.org), whose OS image is signature-verified at build time. It has an immutable `/usr`, automatic A/B OS updates that apply on the next reboot, and no user accounts you can log in to. Host SSH, every login prompt, the serial and debug shells, console autologin and Ctrl-Alt-Del are disabled. The VM console only shows status. Kernel and network settings are hardened, and guest copy, paste and device changes are disabled.
- **Short-lived downloads.** The report server listens only while a report is shared. Each report has its own random 192-bit link, a fresh self-signed certificate is used, and links expire after 24 hours.
- **Minimal container.** The engine image is built `FROM scratch` and holds only a static binary and a CA bundle, about 6 MB compressed. It runs as a non-root user with a read-only file system, no capabilities and `no-new-privileges`, and it never gets access to the Docker socket.
- **Supply chain.** CI tests every change, boots the appliance in QEMU and attacks its console, and scans images with Trivy. Each release has SBOM and provenance attestations, and the installer and OVA are published with checksums.

A vSphere administrator who can manage the appliance VM can read its disks. On the appliance, stored vCenter credentials stay encrypted, but collected utilisation data does not.

Please report vulnerabilities privately through [GitHub security advisories](https://github.com/MarcoColomb0/rightsizer/security/advisories/new).

## Updates

When a release is available, the console offers it with a `[Y/n]` prompt, and `u` reopens it. Every upgrade protects your data:

1. download the new version while collection continues
2. stop the engine cleanly
3. back up the data (the last three backups are kept)
4. start the new version
5. check that it is healthy and has loaded every saved analysis
6. otherwise restore the backup and the previous version automatically

On the appliance, the engine asks the host to perform the upgrade. Your session disconnects while the engine restarts; reconnect after about a minute. The OS updates itself, and the update applies on the next reboot. In the Docker install, the `rightsizer` launcher performs the upgrade and first offers an update for itself.

vCenter keeps one hour of real-time samples, so a short upgrade leaves no gap in the data.

## How recommendations are calculated

Every five minutes rightsizer collects the 20-second real-time samples of each powered-on VM and host. It stores them in fixed-size histograms, so memory use stays flat over a 14-day window and every percentile covers every sample.

| Profile | Percentile | vCPU target | Memory headroom | Memory floor | Host CPU / memory target |
| --- | --- | --- | --- | --- | --- |
| conservative | p99 | 60% | +40% | 50% | 60% / 80% |
| balanced | p95 | 70% | +25% | 35% | 70% / 85% |
| aggressive | p95 | 80% | +10% | 25% | 80% / 90% |

- **vCPU** = ceil(vCPU × CPU percentile ÷ target), minimum 1.
- **Memory** = active-memory percentile × headroom, rounded up to 1 GB. It never goes below the memory floor (as a share of current memory), 1 GB for Linux or 2 GB for Windows.
- **Clusters:** CPU is sized from the demand percentile ÷ host CPU target, and memory from the recommended VM memory plus 5% ÷ host memory target. One HA host is added.

Active memory can understate what databases and JVMs reserve. Check memory reductions against in-guest metrics before applying them.

## Development

```bash
make test       # unit tests and integration tests against the govmomi vCenter simulator
make build      # ./rightsizer
make image      # rightsizer:local
make demo-pdf   # sample report from synthetic data
```

`appliance/smoke-test.sh` boots the appliance in QEMU, and `appliance/build.sh` builds the OVA. Releases are published by pushing a `vX.Y.Z` tag.

## License

[Apache License 2.0](LICENSE) © [Marco Colombo](https://marco.wf)
