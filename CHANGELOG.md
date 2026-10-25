# Changelog

Notable changes to rightsizer. Each section becomes the notes of its GitHub release and is shown in the console's update prompt. Add new entries under **Unreleased**; when tagging, rename it to the version and date. Earlier releases are listed on the [releases page](https://github.com/MarcoColomb0/rightsizer/releases).

## Unreleased

### Added

- NetApp ONTAP clusters (9.10 or later) can be added as sources with a `readonly` user. Their volumes and LUNs are matched to the datastores of every vCenter, and the sizing shows the data reduction measured today, the physical capacity the planned storage needs at that reduction, array Snapshot copies, data in other volumes, and the controllers' load. The sizing PDF has a new ONTAP section and the CSV data adds `ontap-clusters.csv`, `ontap-aggregates.csv` and the ONTAP volume of each datastore.
- Datastores on NetApp LUNs are flagged in the sizing when their ONTAP cluster has not been added.

### Changed

- Pre-releases such as v1.4.0-rc.1 are published as GitHub pre-releases: installs, upgrades and update prompts keep the latest full release, and `install.sh --tag` installs a pre-release by name. An installed pre-release is offered its final release.

## v1.3.1 - 2026-10-25

### Added

- Pre-release channel for the appliance: press `P` on the home screen to have release candidates offered as upgrades, with the usual backup and rollback, and again to leave it. Off by default.

## v1.3.0 - 2026-10-25

### Changed
- Update Flatcar LTS to v4081.3.11
- Update docker/login-action to 81c6f93
- Update VMware govmomi to v0.56.1

## v1.2.1 - 2026-10-21

### Fixed
- Built with Go 1.27.3, which fixes [CVE-2026-111943](https://nvd.nist.gov/vuln/detail/CVE-2026-111943) (

## v1.2.0 - 2026-10-11

### Added

- Collect for longer: `+` on a vCenter adds 1 to 14 days to an analysis, up to 30 days in total. A finished analysis reopens and keeps everything collected so far.

### Fixed

- Built with Go 1.27.2, which fixes denial-of-service vulnerabilities in Go's HTTP server.
- Long text in PDF tables, such as disk paths and suggestions, wraps inside its cell instead of being cut off.
- Release notes list only the changes from the changelog, without the automatically generated list of merged pull requests.

## v1.1.0 - 2026-10-04

### Added

- The update prompt shows what changed in every version since the installed one, with a link to the release page.
- Prompts with two buttons, such as the update prompt and confirmation dialogs, can be answered with the arrow keys and enter. Dialogs open on Cancel, so a stray enter changes nothing.

### Changed

- The install command downloads the installer from the latest release instead of the main branch, so it is always a published, checksummed version.
- The appliance's Flatcar Container Linux release and all dependencies are now kept up to date automatically.

## v1.0.0 - 2026-10-01

### Added

- A redesigned console with source cards, KPI tiles, a findings table with a detail panel, a coloured peak heatmap, dialogs and notifications, full key help on `?` and mouse support.
- Light and dark themes that follow the terminal, with the terminal's tab or taskbar showing collection progress where supported.
- `rightsizer demo` opens the console on synthetic vCenters, with nothing to install or connect.
- Redesigned PDF reports with numbered sections, a document outline, severity badges and highlighted recommendations.

### Changed

- The SSH console runs on an updated SSH server that fixes a published vulnerability in the previous one.
- The README is shorter, with a showcase; details moved to the `docs` folder.
