#!/usr/bin/env bash
# Boots the appliance in QEMU with a simulated vApp environment and checks it
# from the outside: only the rightsizer SSH console is reachable, only the
# administrator can log in, and nothing but the console is offered.
#
#   appliance/smoke-test.sh <engine-image-with-tag>
set -euo pipefail

ENGINE="${1:?usage: smoke-test.sh <engine-image>}"
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/flatcar.env"
WORK="${WORK:-$(mktemp -d)}"
mkdir -p "$WORK"
PW='ci & admin password'
PORT=2222
cleanup() {
	if [ -f "$WORK/qemu.pid" ]; then
		kill "$(cat "$WORK/qemu.pid")" 2>/dev/null || true
	fi
}
trap cleanup EXIT

base="https://stable.release.flatcar-linux.net/amd64-usr/${FLATCAR_VERSION}"
if [ ! -f "$WORK/flatcar.img" ]; then
	curl -fsSL --retry 3 -o "$WORK/flatcar.img" "$base/flatcar_production_qemu_image.img"
	curl -fsSL --retry 3 -o "$WORK/flatcar.img.sig" "$base/flatcar_production_qemu_image.img.sig"
fi
export GNUPGHOME="$WORK/gnupg"
mkdir -p "$GNUPGHOME"
chmod 0700 "$GNUPGHOME"
gpg --batch --quiet --import "$HERE/flatcar-signing-key.asc"
sigstatus="$(gpg --batch --status-fd 1 --verify "$WORK/flatcar.img.sig" "$WORK/flatcar.img" 2>/dev/null || true)"
if ! awk -v fp="$FLATCAR_KEY_FINGERPRINT" '$1 == "[GNUPG:]" && $2 == "VALIDSIG" && $NF == fp { ok = 1 } END { exit !ok }' <<<"$sigstatus"; then
	echo "Flatcar image signature invalid:" >&2
	echo "$sigstatus" >&2
	exit 1
fi

mkdir -p "$WORK/bundle" "$WORK/ign"
docker save "$ENGINE" | gzip -1 >"$WORK/bundle/rightsizer-image.tar.gz"
truncate -s "$(($(du -sm "$WORK/bundle" | cut -f1) + 32))M" "$WORK/bundle.raw"
mkfs.ext4 -q -F -L RSBUNDLE -d "$WORK/bundle" "$WORK/bundle.raw"

sed "s/@IMAGE_TAG@/${ENGINE##*:}/" "$HERE/butane.yaml" | butane --strict -d "$HERE/files" >"$WORK/ign/appliance.ign"
butane --strict -d "$WORK/ign" "$HERE/smoke.bu" >"$WORK/smoke.ign"

qemu-img create -q -f qcow2 -F qcow2 -b "$WORK/flatcar.img" "$WORK/disk.qcow2"
qemu-system-x86_64 -name rightsizer-ci -m 2048 -smp 2 -machine accel=kvm:tcg -cpu max \
	-display none -serial "file:$WORK/serial.log" -daemonize -pidfile "$WORK/qemu.pid" \
	-drive if=virtio,file="$WORK/disk.qcow2" \
	-drive if=virtio,format=raw,file="$WORK/bundle.raw",readonly=on \
	-fw_cfg name=opt/org.flatcar-linux/config,file="$WORK/smoke.ign" \
	-netdev user,id=n0,hostfwd=tcp:127.0.0.1:${PORT}-:22,hostfwd=tcp:127.0.0.1:8443-:443 \
	-device virtio-net-pci,netdev=n0

fail() {
	echo "✗ $*" >&2
	echo "--- serial console (tail)" >&2
	tail -n 60 "$WORK/serial.log" >&2 || true
	exit 1
}

echo "› waiting for the appliance to boot"
banner=""
for _ in $(seq 1 180); do
	banner="$(timeout 3 bash -c "exec 3<>/dev/tcp/127.0.0.1/$PORT && head -n1 <&3" 2>/dev/null | tr -d '\r' || true)"
	[[ "$banner" == SSH-2.0-* ]] && break
	sleep 5
done
[[ "$banner" == SSH-2.0-rightsizer* ]] || fail "no rightsizer SSH console on port 22 (banner: '$banner')"
echo "✓ SSH console answers ($banner)"

ssh_try() {
	local user="$1" pw="$2"
	shift 2
	SSHPASS="$pw" timeout 60 sshpass -e ssh -p "$PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
		-o PreferredAuthentications=password -o PubkeyAuthentication=no -o LogLevel=ERROR "$user@127.0.0.1" "$@" 2>&1
}

for _ in $(seq 1 30); do
	out="$(ssh_try admin "$PW" true || true)"
	[[ "$out" == *"only the interactive console"* ]] && break
	sleep 5
done
[[ "$out" == *"only the interactive console"* ]] || fail "admin login with the vApp password failed: $out"
echo "✓ administrator logs in with the vApp password; commands are refused"

out="$(ssh_try root "$PW" true || true)"
[[ "$out" == *"Permission denied"* ]] || fail "root must not log in: $out"
out="$(ssh_try core "$PW" true || true)"
[[ "$out" == *"Permission denied"* ]] || fail "core must not log in: $out"
echo "✓ root and core cannot log in"

out="$(ssh_try admin "$PW" -s sftp || true)"
[[ "$out" != *"sftp>"* ]] || fail "sftp must not be available"
sshopts=(-p "$PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o PreferredAuthentications=password -o PubkeyAuthentication=no)
out="$(SSHPASS="$PW" timeout 30 sshpass -e ssh "${sshopts[@]}" -o ExitOnForwardFailure=yes -N -R 127.0.0.1:18081:127.0.0.1:22 admin@127.0.0.1 2>&1 || true)"
[[ "$out" == *"forwarding failed"* ]] || fail "remote port forwarding must be refused: $out"
SSHPASS="$PW" timeout 30 sshpass -e ssh "${sshopts[@]}" -N -L 127.0.0.1:18080:127.0.0.1:22 admin@127.0.0.1 2>"$WORK/fwd.log" &
fwd=$!
sleep 8
timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/18080 && head -n1 <&3' >"$WORK/fwd.out" 2>/dev/null || true
sleep 2
kill "$fwd" 2>/dev/null || true
wait "$fwd" 2>/dev/null || true
if grep -q "SSH-2.0" "$WORK/fwd.out"; then
	fail "local port forwarding must be refused"
fi
grep -qiE "open failed|prohibited|refused" "$WORK/fwd.log" || fail "local port forwarding was not rejected: $(cat "$WORK/fwd.log")"
echo "✓ no sftp, no port forwarding"

if timeout 5 bash -c "exec 3<>/dev/tcp/127.0.0.1/8443" 2>/dev/null; then
	(echo | timeout 5 openssl s_client -connect 127.0.0.1:8443 >/dev/null 2>&1) && fail "report server must be closed while nothing is shared"
fi
echo "✓ report server closed while nothing is shared"

for _ in $(seq 1 5); do ssh_try admin "wrong password" true >/dev/null || true; done
out="$(ssh_try admin "$PW" true || true)"
[[ "$out" != *"only the interactive console"* ]] || fail "address must be locked out after repeated failures"
echo "✓ repeated failures lock the address out"

if tr -d '\r' <"$WORK/serial.log" | grep -qE 'login: *$'; then
	fail "the serial console must not offer a login prompt"
fi
echo "✓ no login prompt on the serial console"
echo "✓ appliance smoke test passed"
