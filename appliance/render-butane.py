#!/usr/bin/env python3
"""Print the appliance's first-boot Butane config, generated from
host/manifest so first boot and upgrades install the same host layer.
Butane reads JSON as YAML.

    render-butane.py <engine-image-tag>
"""
import json
import pathlib
import re
import sys

UNIT = re.compile(r"^[a-z0-9@.-]+\.(service|socket|path|timer|target)$")


def main(tag):
    if not re.fullmatch(r"[0-9A-Za-z._-]+", tag):
        sys.exit(f"invalid image tag: {tag}")
    here = pathlib.Path(__file__).resolve().parent
    files, enable, mask, present, absent = [], [], [], [], []
    for n, raw in enumerate((here / "host" / "manifest").read_text().splitlines(), 1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        kind, *rest = line.split()
        if kind == "file" and len(rest) == 2:
            mode, path = rest
            if not (here / "host" / "root" / path.lstrip("/")).is_file():
                sys.exit(f"manifest line {n}: {path} missing from host/root")
            files.append({"path": path, "mode": int(mode, 8), "overwrite": True,
                          "contents": {"local": "root" + path}})
        elif kind in ("enable", "mask") and len(rest) == 1 and UNIT.match(rest[0]):
            (enable if kind == "enable" else mask).append(rest[0])
        elif kind == "kernel" and len(rest) == 1:
            arg = rest[0]
            (absent if arg.startswith("-") else present).append(arg.lstrip("-"))
        else:
            sys.exit(f"manifest line {n}: cannot parse {raw!r}")

    config = {
        "variant": "flatcar",
        "version": "1.1.0",
        "kernel_arguments": {"should_exist": present, "should_not_exist": absent},
        "passwd": {
            "groups": [{"name": "rightsizer", "gid": 65532, "system": True}],
            # Owns the engine data (the container runs as 65532) and runs the
            # VM console screen. No password, no shell, no home.
            "users": [{"name": "rightsizer", "uid": 65532, "primary_group": "rightsizer",
                       "no_user_group": True, "no_create_home": True,
                       "shell": "/sbin/nologin", "system": True}],
        },
        "storage": {
            "directories": [
                {"path": "/opt/rightsizer/bin", "mode": 0o755},
                {"path": "/var/lib/rightsizer", "mode": 0o711},
            ],
            "files": files + [{
                "path": "/etc/rightsizer/engine.env", "mode": 0o644,
                "contents": {"inline": f"IMAGE=ghcr.io/marcocolomb0/rightsizer:{tag}\n"},
            }],
        },
        "systemd": {"units": [{"name": u, "mask": True} for u in mask]
                    + [{"name": u, "enabled": True} for u in enable]},
    }
    print(json.dumps(config, indent=2))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
