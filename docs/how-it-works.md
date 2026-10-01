# How it works

## Collection

Every five minutes rightsizer collects the 20-second real-time samples of each powered-on VM and host. It stores them in fixed-size histograms, so memory use stays flat over a 14-day window and every percentile covers every sample.

When an analysis starts, it imports the last 14 days of history stored in vCenter so first results arrive within minutes. Each stored interval is read only for the period it alone covers (finest first), and every sample is weighted by the time it represents. Each VM switches to 20-second data once it has 24 hours of it.

During the run, rightsizer keeps reading vCenter's 5-minute averages and compares them with the 20-second data for the same period. Reports show how much the averages understate utilisation, list bursty VMs whose peaks the averages hide, and warn when the 14 days before the analysis had a clearly higher peak than the window itself.

## Recommendations

| Profile | Percentile | vCPU target | Memory headroom | Memory floor | Host CPU / memory target |
| --- | --- | --- | --- | --- | --- |
| conservative | p99 | 60% | +40% | 50% | 60% / 80% |
| balanced | p95 | 70% | +25% | 35% | 70% / 85% |
| aggressive | p95 | 80% | +10% | 25% | 80% / 90% |

- **vCPU** = ceil(vCPU × CPU percentile ÷ target), minimum 1.
- **Memory** = active-memory percentile × headroom, rounded up to 1 GB. It never goes below the memory floor (as a share of current memory), 1 GB for Linux or 2 GB for Windows.
- **Clusters:** CPU is sized from the demand percentile ÷ host CPU target, and memory from the recommended VM memory plus 5% ÷ host memory target. One HA host is added.
- **Waste:** orphaned virtual disks no VM uses, VMs wider than a NUMA node, VMs slowed by CPU co-stop, idle VMs, VMs powered off for the whole window, old snapshots, and thick disks that are mostly empty.

Active memory can understate what databases and JVMs reserve. Check memory reductions against in-guest metrics before applying them.

## Peak-aware sizing

VMs rarely peak at the same time. The diversity factor is the sum of each VM's percentile of 30-minute CPU demand ÷ the same percentile of their combined demand: 1.5× means sizing on the sum of peaks asks for 50% more CPU than needed. VMs whose demand correlates at 0.8 or more are reported as peaking together, and at -0.4 or less as peaking at different times. Placement is left to DRS.

## Hardware refresh sizing

The sizing tab and its PDF suggest node shapes (CPUs × cores, RAM) and counts per cluster, the network, FC and out-of-band ports each node needs, and the storage to buy, with the data behind it as CSV. They also inventory today's hosts, adapters, link speeds, storage MTU, LUN backing and cluster settings, and flag what the refresh must handle: CPU vendor changes, the largest VMs, vGPU and passthrough devices, raw device mappings and reservations.

The sizing options (`o`) apply to every vCenter and are kept across upgrades: compute basis (as provisioned, the default, or rightsized), powered-off VMs in or out, growth (20%), vCPU per core (automatic: today's ratio, at least 4:1), CPU and memory targets with the HA spares out (70% and 90%), HA spares per cluster (1), sockets per node (2), per-core uplift of the new CPUs (0%), storage kept free (20%) and workload groups by VM name pattern (for example `databases=sql*,*ora*; vdi=vdi-*`, otherwise by guest OS).

- **Cores** = the larger of vCPU ÷ vCPU per core, and measured CPU demand ÷ CPU target ÷ today's per-core clock (raised by the uplift). **RAM** = configured memory + 5% ÷ memory target, without overcommit.
- **Node options** use 16 to 128 cores per CPU and 256 GB to 4 TB of RAM, and must hold the largest VM. Below 16 cores per CPU is not suggested, because vSphere is licensed per core with at least 16 counted per CPU. The recommended option has the fewest servers among those within 10% of the lowest total cores, preferring nodes where the largest VM fits in one socket.
- **Ports** per node: 2 × 25 GbE and 2 × 32G FC (or 2 Ethernet storage ports for NFS, iSCSI, NVMe/TCP and vSAN), plus 1 × 1 GbE out-of-band. A faster speed is suggested when today's adapters are faster or the measured peak per node would fill more than 40-50% of the pair.
- **Raw used storage** = VM disks + snapshots + other VM files + templates + raw device mappings, as vSphere sees them, before any data reduction. Swap files (recreated at power-on) and orphaned disks are listed but not included. **Planned usable** = raw used × growth ÷ (1 − free space). Apply the data reduction you expect for each workload type to these figures.
- **Storage performance** sums every host's 20-second datastore counters at each sample, so the percentile and peak IOPS, MB/s and latency describe all datastores at once.
