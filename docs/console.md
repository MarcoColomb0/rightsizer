# Using the console

## Add a vCenter

1. Press `a`. Enter the vCenter address, a user with the built-in **Read-only** role, the duration (24 hours to 14 days) and a sizing profile. To also find orphaned disks, give that role the **Datastore > Browse datastore** privilege; everything else works without it.
2. Compare the certificate fingerprint with the one shown in vCenter, then press `y`.
3. Repeat for other vCenters, then leave with `q`. Collection continues in the background.

First results appear within minutes: rightsizer imports the last 14 days of history stored in vCenter. Results marked **preview** come from those stored averages (5-minute to 2-hour samples), which smooth out short peaks, so treat them as a first look. Each VM switches to 20-second data once it has 24 hours of it.

## Keys

`?` shows every key for the current screen, and the mouse works too: click a vCenter, a tab, a finding or a button, and scroll with the wheel.

| Home | |
| --- | --- |
| `enter` | open a vCenter |
| `a` | add a vCenter |
| `p` | combined PDF report of every vCenter |
| `z` | combined hardware refresh sizing PDF and CSV data |
| `o` | sizing options |
| `x` | exclusions |
| `s` | stop sharing reports |
| `c` | change the administrator password (appliance) |
| `u` | upgrade, when a release is available |

| Inside a vCenter | |
| --- | --- |
| `1` `2` `3` `4` | overview, findings, peak analysis, hardware refresh sizing |
| `p` | PDF report; on the sizing tab, the sizing PDF and its CSV data |
| `f` | finish the analysis early |
| `r` | resume a paused analysis |
| `x` | remove the vCenter with its data |

| Findings tab | |
| --- | --- |
| `/` and `?` | search forward and backward as in vim: incremental, and case-insensitive unless the pattern has capitals |
| `n` and `N` | next and previous match |
| `e` | exclude the selected VM or disk |

## Reports

`p` builds a PDF and shares it on a temporary HTTPS link shown in the console; open it from any browser on the network. When an analysis ends, its final report is shared automatically. Links expire after 24 hours.

## Exclusions

Leave a VM, a name pattern (such as `citrix-*`) or an orphaned disk out of the recommendations, with a reason, a note and an optional review date. Exclusions follow VMs through renames, are kept across upgrades, apply to future analyses and are listed in every report.

## Terminals

The console adapts to light and dark terminals and looks best with true colour. [Ghostty](https://ghostty.org), [WezTerm](https://wezterm.org), [kitty](https://sw.kovidgoyal.net/kitty/) and [iTerm2](https://iterm2.com) on macOS and Linux, and [Windows Terminal](https://aka.ms/terminal) on Windows, also show collection progress in the tab or taskbar and make report links clickable. The macOS Terminal app works with fewer colours.

Over SSH, terminals that report themselves as `xterm-256color` get 256 colours. Add `SetEnv COLORTERM=truecolor` to the appliance's entry in `~/.ssh/config` for full colour.
