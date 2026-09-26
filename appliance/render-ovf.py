#!/usr/bin/env python3
"""Fills in the OVF descriptor template.

    render-ovf.py <template> <output> <version> <flatcar-version> <system.vmdk> \
        <bundle.vmdk> <system-capacity> <bundle-capacity> <ignition.json>
    render-ovf.py --check <template>

--check renders the template with sample values and verifies the result, so
CI catches a broken template on every push instead of at release time.
Literal braces in the template are written {{ and }}.
"""
import base64
import os
import sys
import tempfile
import xml.etree.ElementTree as ET


def render(tpl, out, version, flatcar, system, bundle, sys_cap, bun_cap, ignition):
    with open(ignition, "rb") as f:
        ign = base64.b64encode(f.read()).decode()
    with open(tpl) as f:
        ovf = f.read().format(
            version=version, version_number=version.lstrip("v"), flatcar_version=flatcar,
            system_size=os.path.getsize(system), bundle_size=os.path.getsize(bundle),
            system_capacity=sys_cap, bundle_capacity=bun_cap, ignition=ign,
        )
    with open(out, "w") as f:
        f.write(ovf)


def check(tpl):
    with tempfile.TemporaryDirectory() as d:
        for name, content in (("system.vmdk", b"s"), ("bundle.vmdk", b"b"), ("ignition.json", b"{}")):
            with open(os.path.join(d, name), "wb") as f:
                f.write(content)
        out = os.path.join(d, "rightsizer.ovf")
        render(tpl, out, "v0.0.0", "0.0.0", os.path.join(d, "system.vmdk"), os.path.join(d, "bundle.vmdk"),
               "1", "1", os.path.join(d, "ignition.json"))
        root = ET.parse(out).getroot()
    ns = "{http://schemas.dmtf.org/ovf/envelope/1}"
    props = {p.get(ns + "key"): p for p in root.iter(ns + "Property")}
    channel = props.get("guestinfo.rightsizer.os_channel")
    if channel is None or channel.get(ns + "qualifiers") != 'ValueMap{"lts","stable"}':
        sys.exit("render-ovf: the OS channel property did not render as expected")
    if props.get("guestinfo.ignition.config.data") is None:
        sys.exit("render-ovf: the Ignition property is missing")
    print(f"render-ovf: {tpl} renders to valid OVF with {len(props)} properties")


if __name__ == "__main__":
    if sys.argv[1:2] == ["--check"]:
        check(sys.argv[2])
    else:
        render(*sys.argv[1:])
