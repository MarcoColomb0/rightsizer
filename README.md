# rightsizer

**Size your next hardware refresh on what your VMs use, not on what they were given.**

rightsizer watches a VMware vCenter for 24 hours to 14 days, compares each VM's provisioned vCPU, memory and storage with its measured use, and tells you what the environment actually needs. It never changes anything in vCenter.

[![ci](https://github.com/MarcoColomb0/rightsizer/actions/workflows/ci.yml/badge.svg)](https://github.com/MarcoColomb0/rightsizer/actions/workflows/ci.yml)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

## Features

- **VM rightsizing:** oversized and undersized vCPU and memory, with a confidence level for each recommendation.
- **Refresh sizing:** required GHz, cores, RAM and hosts per cluster, including an HA spare. The figures are hardware-neutral, so you can apply them to any server model.
- **Waste:** idle VMs, VMs powered off for the whole window, old snapshots, and thick disks that are mostly empty.
- **Live console:** a terminal UI shows progress and findings while data is collected.
- **PDF report:** executive summary, per-cluster charts, prioritised findings and methodology.

## Requirements

- A Linux host with [Docker Engine](https://docs.docker.com/engine/install/). Follow the guide for your distribution: [Ubuntu](https://docs.docker.com/engine/install/ubuntu/), [Debian](https://docs.docker.com/engine/install/debian/), [RHEL](https://docs.docker.com/engine/install/rhel/), [Fedora](https://docs.docker.com/engine/install/fedora/) or [others](https://docs.docker.com/engine/install/#supported-platforms).
- HTTPS access from that host to vCenter on port 443.
- A vCenter user with the built-in **Read-only** role.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/MarcoColomb0/rightsizer/main/install.sh | sudo bash
```

| Option | Default | |
| --- | --- | --- |
| `--port` | `8443` | HTTPS port for report downloads |
| `--host` | primary IP | host name or IP used in download links |
| `--bind` | `0.0.0.0` | address the download port listens on |
| `--tag` | latest release | release to install, e.g. `v0.1.0` |
| `--build` | | build the image from source instead of pulling it |
| `--no-update-check` | | never look for new releases |

## Usage

```bash
rightsizer
```

1. Enter the vCenter address, the read-only user and password, the duration and a sizing profile.
2. Compare the certificate fingerprint with the one shown in vCenter, then press `y`.
3. Press `q` to leave. Collection continues in the background; run `rightsizer` again to check on it.

When the window ends, the final report is built and a download link appears in the console. Press `p` at any time for an interim report.

| Command | |
| --- | --- |
| `rightsizer` | open the console |
| `rightsizer status` | one-line status |
| `rightsizer export [file.pdf]` | copy the latest report to the current directory |
| `rightsizer backup` | back up collected data |
| `rightsizer update` | update to the latest release |
| `rightsizer logs` | show collector logs |
| `rightsizer uninstall` | remove rightsizer (collected data optional) |

## Security

- **Read-only by construction.** Every vSphere call passes an allowlist of read methods, and anything else is blocked before it leaves the process. A test checks that power and delete operations are refused.
- **Certificate pinning.** Self-signed vCenter certificates are accepted only after you confirm their SHA-256 fingerprint. After that, any other certificate is refused.
- **No stored passwords.** The vCenter password is kept only in memory. After a restart the console asks for it again. Collection pauses after three failed logins, so a changed password can't lock the account.
- **Short-lived downloads.** The report server starts only when a report is ready. It serves one file over HTTPS behind a random 192-bit link and stops after 24 hours.
- **Minimal container.** The image is built `FROM scratch` and holds only a static binary and a CA bundle, about 6 MB compressed. It runs as a non-root user with a read-only file system, no capabilities and `no-new-privileges`.
- **Supply chain.** Images are multi-arch and scanned with Trivy. Each release has SBOM and provenance attestations, and the installer is published with checksums.

Please report vulnerabilities privately through [GitHub security advisories](https://github.com/MarcoColomb0/rightsizer/security/advisories/new).

## Updates

When a release is available, `rightsizer` offers it on start:

- **Launcher:** a `[Y/n]` prompt before the console opens. The new installer is checked against the release checksums and, if the GitHub CLI is logged in, against its build attestation.
- **Container:** a `[Y/n]` dialog in the console (also `u`). The upgrade shows its progress and protects your data:
  1. downloads the new image while collection continues
  2. stops the collector cleanly
  3. backs up the data (the last three backups are kept)
  4. starts the new version
  5. checks that it is healthy and has loaded the saved analysis
  6. otherwise restores the backup and the previous version automatically

A running analysis continues after an upgrade once you re-enter the vCenter password. vCenter keeps one hour of real-time samples, so a short upgrade leaves no gap.

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

Releases are published by pushing a `vX.Y.Z` tag.

## License

[Apache License 2.0](LICENSE) © [Marco Colombo](https://marco.wf)
