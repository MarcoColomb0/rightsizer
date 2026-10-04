# Changelog

Notable changes to rightsizer. Each section becomes the notes of its GitHub release and is shown in the console's update prompt. Add new entries under **Unreleased**; when tagging, rename it to the version and date. Earlier releases are listed on the [releases page](https://github.com/MarcoColomb0/rightsizer/releases).

## Unreleased

### Added

- The update prompt shows what changed in every version since the installed one, with a link to the release page.

### Changed

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
