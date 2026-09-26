#!/usr/bin/env bash
# Boots the appliance in QEMU with a simulated vApp environment and checks it
# from the outside: only the rightsizer SSH console is reachable, only the
# administrator can log in, and nothing but the console is offered.
#
#   appliance/smoke-test.sh <engine-image> [upgrade-image]
#   appliance/smoke-test.sh --fetch     # only download the Flatcar image
#
# With an upgrade image (tag 0.0.1, whose host bundle adds a kernel
# argument), the test also upgrades the running appliance and restarts it.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/flatcar.env"
WORK="${WORK:-$(mktemp -d)}"
mkdir -p "$WORK"
base="https://${FLATCAR_CHANNEL}.release.flatcar-linux.net/amd64-usr/${FLATCAR_VERSION}"
fetch() {
	if [ ! -f "$WORK/flatcar.img" ]; then
		curl -fsSL --retry 3 -o "$WORK/flatcar.img.part" "$base/flatcar_production_qemu_image.img"
		curl -fsSL --retry 3 -o "$WORK/flatcar.img.sig" "$base/flatcar_production_qemu_image.img.sig"
		mv "$WORK/flatcar.img.part" "$WORK/flatcar.img"
	fi
}
# --fetch only downloads the Flatcar image, so CI can do it while it builds;
# the signature is still verified below before the image is used.
if [ "${1:-}" = "--fetch" ]; then
	fetch
	exit 0
fi
ENGINE="${1:?usage: smoke-test.sh <engine-image> [upgrade-image] | --fetch}"
UPGRADE="${2:-}"
PW='ci & admin password'
PORT=2222
cleanup() {
	if [ -f "$WORK/qemu.pid" ]; then
		kill "$(cat "$WORK/qemu.pid")" 2>/dev/null || true
	fi
}
trap cleanup EXIT

fetch
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
if [ -n "$UPGRADE" ]; then
	docker save "$UPGRADE" | gzip -1 >"$WORK/bundle/rightsizer-image-upgrade.tar.gz"
fi
truncate -s "$(($(du -sm "$WORK/bundle" | cut -f1) + 32))M" "$WORK/bundle.raw"
mkfs.ext4 -q -F -L RSBUNDLE -d "$WORK/bundle" "$WORK/bundle.raw"

python3 "$HERE/render-butane.py" "${ENGINE##*:}" | butane --strict -d "$HERE/host" >"$WORK/ign/appliance.ign"
butane --strict -d "$WORK/ign" "$HERE/smoke.bu" >"$WORK/smoke.ign"

qemu-img create -q -f qcow2 -F qcow2 -b "$WORK/flatcar.img" "$WORK/disk.qcow2"
qemu-system-x86_64 -name rightsizer-ci -m 2048 -smp 2 -machine accel=kvm:tcg -cpu max \
	-display none -serial "file:$WORK/serial.log" -daemonize -pidfile "$WORK/qemu.pid" \
	-monitor "unix:$WORK/monitor.sock,server=on,wait=off" \
	-drive if=virtio,file="$WORK/disk.qcow2" \
	-drive if=virtio,format=raw,file="$WORK/bundle.raw",readonly=on \
	-fw_cfg name=opt/org.flatcar-linux/config,file="$WORK/smoke.ign" \
	-netdev user,id=n0,hostfwd=tcp:127.0.0.1:${PORT}-:22,hostfwd=tcp:127.0.0.1:8443-:443,hostfwd=tcp:127.0.0.1:19901-:9901,hostfwd=tcp:127.0.0.1:19902-:9902 \
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

if tr -d '\r' <"$WORK/serial.log" | grep -qE 'login: *$'; then
	fail "the serial console must not offer a login prompt"
fi
echo "✓ no login prompt on the serial console"

# The VM console (what vCenter's web console shows) must display the status
# screen, not boot logs or a login prompt.
screen() {
	python3 - "$WORK/monitor.sock" "$WORK/screen.ppm" <<'PY'
import socket, sys, time
s = socket.socket(socket.AF_UNIX)
s.connect(sys.argv[1])
s.settimeout(5)
time.sleep(0.5)
try:
    s.recv(65536)
except OSError:
    pass
s.sendall(("screendump %s\n" % sys.argv[2]).encode())
time.sleep(2)
PY
	convert "$WORK/screen.ppm" -scale 200% "$WORK/screen.png"
	tesseract "$WORK/screen.png" - 2>/dev/null
}
text=""
for _ in $(seq 1 24); do
	text="$(screen || true)"
	if grep -qi "rightsizer appliance" <<<"$text" && grep -qi "ssh admin@" <<<"$text" &&
		grep -qi "running" <<<"$text" && grep -qi "SSH key" <<<"$text"; then
		break
	fi
	sleep 5
done
if ! grep -qi "rightsizer appliance" <<<"$text" || ! grep -qi "ssh admin@" <<<"$text" ||
	! grep -qi "running" <<<"$text" || ! grep -qi "SSH key" <<<"$text"; then
	fail "the VM console does not show the status screen. OCR read:
$text"
fi
if grep -qiE "login:|audit|systemd\[" <<<"$text"; then
	fail "the VM console shows a login prompt or log lines. OCR read:
$text"
fi
echo "✓ VM console shows the status screen: address, SSH key fingerprint, engine running"
# OCR confuses l with I, 1 or |.
channel=${FLATCAR_CHANNEL//l/[lI1|]}
if ! grep -qiE "${FLATCAR_VERSION//./\\.}.*${channel} updates" <<<"$text"; then
	fail "the VM console must show Flatcar ${FLATCAR_VERSION} on the ${FLATCAR_CHANNEL} channel. OCR read:
$text"
fi
echo "✓ appliance runs Flatcar ${FLATCAR_VERSION} and updates from the ${FLATCAR_CHANNEL} channel"

# Runs last: QEMU's user networking makes every client the same address,
# so the lockout would block the logins the other checks need.
lockout() {
	for _ in $(seq 1 5); do ssh_try admin "wrong password" true >/dev/null || true; done
	out="$(ssh_try admin "$PW" true || true)"
	[[ "$out" != *"only the interactive console"* ]] || fail "address must be locked out after repeated failures"
	echo "✓ repeated failures lock the address out"
}

if [ -z "$UPGRADE" ]; then
	lockout
	echo "✓ appliance smoke test passed"
	exit 0
fi

trigger() { timeout 5 bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" 2>/dev/null || true; }

wait_screen() {
	local want="$1" not="${2:-}"
	for _ in $(seq 1 60); do
		text="$(screen || true)"
		if grep -qiE "$want" <<<"$text" && { [ -z "$not" ] || ! grep -qiE "$not" <<<"$text"; }; then
			return 0
		fi
		sleep 5
	done
	return 1
}

login_ok() {
	for _ in $(seq 1 40); do
		out="$(ssh_try admin "$PW" true || true)"
		[[ "$out" == *"only the interactive console"* ]] && return 0
		sleep 5
	done
	return 1
}

# OCR often reads the zeros in the version as the letter O.
V001='v[0O]\.[0O]\.1'
trigger 19901
wait_screen "$V001" || fail "the appliance did not upgrade to v0.0.1. OCR read:
$text"
wait_screen "restart.*required|required.*kernel" || fail "the kernel change did not raise a restart flag. OCR read:
$text"
echo "✓ upgrade applied the new appliance host layer and asks for a restart"
login_ok || fail "the administrator can no longer log in after the upgrade: $out"
echo "✓ administrator password and stored data survived the upgrade"

trigger 19902
sleep 20
wait_screen "$V001" "restart.*required" || fail "after the restart the flag must clear. OCR read:
$text"
login_ok || fail "the administrator cannot log in after the restart"
echo "✓ restart request rebooted the appliance and cleared the flag"

lockout
echo "✓ appliance smoke test passed"
