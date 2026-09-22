# rightsizer

Read-only vSphere rightsizing. rightsizer watches a vCenter for 24 hours to 14 days, compares what every VM is **provisioned** with what it actually **uses**, and tells you how much CPU, memory, storage and how many hosts you really need, so hardware refreshes can be sized on evidence rather than on current allocations.

Open source by [Marco Colombo](https://marco.wf) · Apache-2.0

## What it finds

| Area | Finding |
| --- | --- |
| VM rightsizing | Oversized and undersized vCPU and memory, from 20-second samples, with confidence levels |
| Cluster / refresh sizing | Required GHz, cores, RAM and hosts per cluster (N+1), hardware-neutral |
| Waste | Idle VMs, VMs powered off for the whole window |
| Storage | Old snapshots and their size, thick disks with mostly empty guest file systems |

Results are live in the terminal console while data is collected, and exported as a PDF report.

## Safety

* **It never changes anything.** Every vSphere API call goes through an allowlist of read-only methods (property retrieval and performance queries); anything else is refused inside the tool before it is sent. A test proves `PowerOffVM_Task` and `Destroy_Task` are blocked.
* Use a vCenter user with the built-in **Read-only** role. No other privilege is needed.
* Self-signed vCenter certificates are supported through trust on first use: the console shows the SHA-256 fingerprint, and once you accept it that certificate is pinned. Any other certificate is refused.
* The password is kept in memory only, never on disk. If the container restarts, the console asks for it again. After three rejected logins collection pauses, so a changed password cannot lock the account.
* PDF downloads use a temporary HTTPS server that starts only when a report is ready, serves a single file behind a random 192-bit link with a fresh self-signed certificate, and stops after 24 hours.
* The image is built `FROM scratch` and holds only the static binary and a CA bundle (about 6 MB compressed). The container runs as a non-root user with a read-only root file system, all capabilities dropped and `no-new-privileges`.

## Install

Requirements: a Linux host with Docker, HTTPS access to vCenter (443), and outbound access to `ghcr.io` or GitHub.

```bash
curl -fsSL https://raw.githubusercontent.com/MarcoColomb0/rightsizer/main/install.sh | sudo bash
```

Options: `--port 8443` (report download port), `--host name` (host name used in download links), `--bind 127.0.0.1`, `--tag v1.0.0`, `--build` (build the image from source).

## Use

```bash
rightsizer
```

1. Enter vCenter, a read-only user, the password, the duration (14 days recommended) and a sizing profile.
2. Check the certificate fingerprint and press `y`.
3. Press `q` to detach. Collection continues in the background. Run `rightsizer` again at any time to follow progress.

When the window ends, the final PDF is built and a download link appears in the console. Press `p` at any time for an interim report.

| Command | |
| --- | --- |
| `rightsizer` | open the console |
| `rightsizer status` | one-line status |
| `rightsizer export [file.pdf]` | copy the latest report to the host |
| `rightsizer logs` | collector logs |
| `rightsizer update` | update launcher and container to the latest release |
| `rightsizer backup` | back up collected data to the `rightsizer-backups` volume |
| `rightsizer uninstall` | remove container, wrapper and (optionally) data |

## Updates

When a new release is published, `rightsizer` offers it on start:

1. **Launcher** (the `rightsizer` command on the host): a `[Y/n]` prompt before the console opens. The new `install.sh` is checked against the release `SHA256SUMS` and, if the GitHub CLI is logged in, against its GitHub build attestation.
2. **Container** (collector and console): a `[Y/n]` dialog in the console, also reachable with `u`. The upgrade then runs on the host with step-by-step progress:
   1. download the new image while the collector keeps running (provenance verified when the GitHub CLI is logged in)
   2. stop the collector cleanly, so every sample is written to disk
   3. back up `/data` to the `rightsizer-backups` volume (last 3 kept)
   4. start the new version
   5. check it is healthy and loaded the saved analysis
   6. otherwise remove it, restore the backup and restart the previous container

The container never gets access to the Docker socket; upgrades are always performed by the launcher. An analysis in progress continues after an upgrade once the vCenter password is entered again. vCenter keeps one hour of real-time samples, so no data is lost. Disable checks with `--no-update-check` or `RIGHTSIZER_NO_UPDATE_CHECK=1`. The release lookup is one anonymous request to the GitHub API, cached for six hours.

Releases are cut by pushing a `vX.Y.Z` tag: CI tests, builds and scans the multi-arch image, attests it, then publishes the release with `install.sh` and `SHA256SUMS`.

## How it sizes

Every 5 minutes rightsizer downloads the 20-second real-time samples of every powered-on VM and host and folds them into fixed-size histograms, so memory use stays constant however long the window is, and percentiles use every sample instead of vCenter's averaged roll-ups.

| Profile | Percentile | vCPU target | Memory headroom | Minimum memory kept | Host CPU / memory target |
| --- | --- | --- | --- | --- | --- |
| conservative | p99 | 60% | +40% | 50% | 60% / 80% |
| balanced | p95 | 70% | +25% | 35% | 70% / 85% |
| aggressive | p95 | 80% | +10% | 25% | 80% / 90% |

* **vCPU** = ceil(vCPU × CPU percentile ÷ target), minimum 1. CPU p99 ≥ 90% means undersized.
* **Memory** = active memory percentile × headroom, rounded up to 1 GB, never below the minimum kept, 1 GB (Linux) or 2 GB (Windows).
* **Clusters**: CPU need = cluster demand percentile ÷ host CPU target; memory need = recommended VM memory + 5% ÷ host memory target; plus one HA host.

Active memory can understate what databases and JVMs reserve, so check memory reductions with in-guest metrics before you apply them.

## Build from source

```bash
make test        # unit and integration tests against the govmomi vCenter simulator
make build       # ./rightsizer
make image       # rightsizer:local
make demo-pdf    # sample report from synthetic data
```

## License

Apache License 2.0. Copyright Marco Colombo.
