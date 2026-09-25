#!/usr/bin/env bash
# Builds the rightsizer OVA from the signed Flatcar VMware image, the engine
# image and the Ignition config. Requires: curl gpg docker qemu-img mkfs.ext4
# butane python3.
#
#   appliance/build.sh v1.2.3 [engine-image]
set -euo pipefail

VERSION="${1:?usage: build.sh vX.Y.Z [engine-image]}"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "invalid version: $VERSION" >&2; exit 1; }
ENGINE="${2:-ghcr.io/marcocolomb0/rightsizer:${VERSION#v}}"
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/flatcar.env"
OUT="${OUT:-$HERE/../dist}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT"

fetch() { curl -fsSL --proto '=https' --tlsv1.2 --retry 3 "$@"; }

echo "› Flatcar ${FLATCAR_VERSION} (${FLATCAR_CHANNEL})"
base="https://${FLATCAR_CHANNEL}.release.flatcar-linux.net/amd64-usr/${FLATCAR_VERSION}"
fetch -o "$WORK/flatcar.ova" "$base/flatcar_production_vmware_ova.ova"
fetch -o "$WORK/flatcar.ova.sig" "$base/flatcar_production_vmware_ova.ova.sig"
export GNUPGHOME="$WORK/gnupg"
mkdir -m 0700 "$GNUPGHOME"
gpg --batch --quiet --import "$HERE/flatcar-signing-key.asc"
sigstatus="$(gpg --batch --status-fd 1 --verify "$WORK/flatcar.ova.sig" "$WORK/flatcar.ova" 2>/dev/null || true)"
if ! awk -v fp="$FLATCAR_KEY_FINGERPRINT" '$1 == "[GNUPG:]" && $2 == "VALIDSIG" && $NF == fp { ok = 1 } END { exit !ok }' <<<"$sigstatus"; then
	echo "Flatcar image signature invalid:" >&2
	echo "$sigstatus" >&2
	exit 1
fi
echo "✓ Flatcar image signature verified"

tar -xf "$WORK/flatcar.ova" -C "$WORK" --wildcards '*.vmdk'
mv "$WORK"/flatcar_production_vmware_ova_image.vmdk "$WORK/rightsizer-system.vmdk"
system_capacity="$(qemu-img info --output=json "$WORK/rightsizer-system.vmdk" | python3 -c 'import json,sys; print(json.load(sys.stdin)["virtual-size"])')"

echo "› Engine image ${ENGINE}"
mkdir -p "$WORK/bundle"
docker image inspect "$ENGINE" >/dev/null 2>&1 || docker pull -q "$ENGINE" >/dev/null
docker save "$ENGINE" | gzip -9 >"$WORK/bundle/rightsizer-image.tar.gz"
echo "$VERSION" >"$WORK/bundle/VERSION"
size_mb=$(($(du -sm "$WORK/bundle" | cut -f1) + 32))
truncate -s "${size_mb}M" "$WORK/bundle.raw"
mkfs.ext4 -q -L RSBUNDLE -d "$WORK/bundle" "$WORK/bundle.raw"
qemu-img convert -O vmdk -o subformat=streamOptimized "$WORK/bundle.raw" "$WORK/rightsizer-bundle.vmdk"
bundle_capacity=$((size_mb * 1024 * 1024))

echo "› Ignition"
python3 "$HERE/render-butane.py" "${ENGINE##*:}" | butane --strict -d "$HERE/host" >"$WORK/ignition.json"
cp "$WORK/ignition.json" "$OUT/rightsizer-${VERSION}.ign"

echo "› OVF"
python3 - "$HERE/rightsizer.ovf" "$WORK" "$VERSION" "$FLATCAR_VERSION" "$system_capacity" "$bundle_capacity" <<'EOF'
import base64, os, sys
tpl, work, version, flatcar, sys_cap, bun_cap = sys.argv[1:]
ign = base64.b64encode(open(os.path.join(work, "ignition.json"), "rb").read()).decode()
ovf = open(tpl).read().format(
    version=version, version_number=version.lstrip("v"), flatcar_version=flatcar,
    system_size=os.path.getsize(os.path.join(work, "rightsizer-system.vmdk")),
    bundle_size=os.path.getsize(os.path.join(work, "rightsizer-bundle.vmdk")),
    system_capacity=sys_cap, bundle_capacity=bun_cap, ignition=ign,
)
open(os.path.join(work, "rightsizer.ovf"), "w").write(ovf)
EOF

(
	cd "$WORK"
	for f in rightsizer.ovf rightsizer-system.vmdk rightsizer-bundle.vmdk; do
		echo "SHA256($f)= $(sha256sum "$f" | cut -d' ' -f1)"
	done >rightsizer.mf
	tar --format=ustar --owner=0 --group=0 -cf "$OUT/rightsizer-${VERSION}.ova" \
		rightsizer.ovf rightsizer.mf rightsizer-system.vmdk rightsizer-bundle.vmdk
)
echo "✓ $OUT/rightsizer-${VERSION}.ova ($(du -h "$OUT/rightsizer-${VERSION}.ova" | cut -f1))"
