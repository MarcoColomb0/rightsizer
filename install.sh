#!/usr/bin/env bash
# rightsizer installer - https://github.com/MarcoColomb0/rightsizer
set -euo pipefail

REPO="MarcoColomb0/rightsizer"
IMAGE="${RIGHTSIZER_IMAGE:-ghcr.io/marcocolomb0/rightsizer}"
TAG="${RIGHTSIZER_TAG:-latest}"
PORT="${RIGHTSIZER_PORT:-8443}"
BIND="${RIGHTSIZER_BIND:-0.0.0.0}"
HOST="${RIGHTSIZER_HOST:-}"
BUILD=0
CONF_DIR=/etc/rightsizer
CONF="${CONF_DIR}/rightsizer.env"
BIN=/usr/local/bin/rightsizer

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '\033[36m›\033[0m %s\n' "$*"; }
die() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<EOF
Usage: install.sh [options]

  --port N       HTTPS port for report downloads (default 8443)
  --bind ADDR    address the download port binds to (default 0.0.0.0)
  --host NAME    host name or IP shown in download links (default: primary IP)
  --tag TAG      image tag to install (default latest)
  --build        build the image from source instead of pulling it
  -h, --help     show this help
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--port) PORT="${2:?}"; shift 2 ;;
	--bind) BIND="${2:?}"; shift 2 ;;
	--host) HOST="${2:?}"; shift 2 ;;
	--tag) TAG="${2:?}"; shift 2 ;;
	--build) BUILD=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage; die "unknown option: $1" ;;
	esac
done

[ "$(uname -s)" = "Linux" ] || die "rightsizer supports Linux hosts only."
[ "$(id -u)" -eq 0 ] || die "run as root, e.g.: curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | sudo bash"
command -v docker >/dev/null 2>&1 || die "Docker is required. Install it first: https://docs.docker.com/engine/install/"
docker info >/dev/null 2>&1 || die "Docker is installed but the daemon is not running (try: systemctl start docker)."

[[ "$PORT" =~ ^[0-9]+$ ]] && [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "invalid port: $PORT"
[[ "$TAG" =~ ^[A-Za-z0-9._-]+$ ]] || die "invalid tag: $TAG"
if [ -z "$HOST" ]; then
	HOST="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')"
	[ -n "$HOST" ] || HOST="$(hostname -I 2>/dev/null | awk '{print $1}')"
	[ -n "$HOST" ] || HOST="$(hostname)"
fi
[[ "$HOST" =~ ^[A-Za-z0-9.:-]+$ ]] || die "invalid host: $HOST"
TZ_NAME="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
[ -n "$TZ_NAME" ] || TZ_NAME="$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || true)"
[[ "$TZ_NAME" =~ ^[A-Za-z0-9/_+-]+$ ]] || TZ_NAME=UTC

bold "Installing rightsizer"

REF="${IMAGE}:${TAG}"
if [ "$BUILD" -eq 0 ]; then
	info "Pulling ${REF}"
	if ! docker pull -q "$REF" >/dev/null; then
		info "Image not available, building from source instead"
		BUILD=1
	fi
fi
if [ "$BUILD" -eq 1 ]; then
	SRC="https://github.com/${REPO}.git#main"
	[ "$TAG" = "latest" ] || SRC="https://github.com/${REPO}.git#${TAG}"
	REF="rightsizer:local"
	info "Building ${REF} from ${SRC}"
	DOCKER_BUILDKIT=1 docker build -q --build-arg "VERSION=${TAG}" -t "$REF" "$SRC" >/dev/null
fi

install -d -m 0755 "$CONF_DIR"
umask 022
cat >"$CONF" <<EOF
IMAGE=${REF}
PORT=${PORT}
BIND=${BIND}
HOST=${HOST}
TZ_NAME=${TZ_NAME}
EOF

cat >"$BIN" <<'WRAPPER'
#!/usr/bin/env bash
set -euo pipefail
C=rightsizer
CONF=/etc/rightsizer/rightsizer.env
[ -r "$CONF" ] || { echo "rightsizer: $CONF missing, re-run the installer" >&2; exit 1; }
# shellcheck disable=SC1090
. "$CONF"

create() {
	docker rm -f "$C" >/dev/null 2>&1 || true
	docker volume create rightsizer-data >/dev/null
	docker run -d --name "$C" \
		--restart unless-stopped \
		--read-only \
		--cap-drop ALL \
		--security-opt no-new-privileges:true \
		--pids-limit 256 \
		--memory 1g \
		--tmpfs /tmp:rw,noexec,nosuid,size=16m \
		-p "${BIND}:${PORT}:8443" \
		-e RIGHTSIZER_PUBLIC_HOST="$HOST" \
		-e RIGHTSIZER_PUBLIC_PORT="$PORT" \
		-e TZ="${TZ_NAME:-UTC}" \
		-v rightsizer-data:/data \
		--log-opt max-size=10m --log-opt max-file=3 \
		"$IMAGE" daemon >/dev/null
}

case "${1:-tui}" in
tui)
	docker exec -it -e TERM="${TERM:-xterm-256color}" -e COLORTERM="${COLORTERM:-}" "$C" /rightsizer tui
	;;
status | version)
	docker exec "$C" /rightsizer "$1"
	;;
export)
	out="${2:-rightsizer-report-$(date +%Y%m%d-%H%M).pdf}"
	docker exec "$C" /rightsizer export >"$out.tmp" && mv "$out.tmp" "$out" && chmod 600 "$out" && echo "saved $out"
	;;
logs)
	docker logs -f --tail 200 "$C"
	;;
start | stop | restart)
	docker "$1" "$C" >/dev/null && echo "rightsizer ${1}ed"
	;;
update)
	[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
	case "$IMAGE" in
	*:local) echo "image was built from source; re-run install.sh --build to update" >&2; exit 1 ;;
	esac
	docker pull -q "$IMAGE" >/dev/null
	create
	echo "rightsizer updated; collected data is kept. Run 'rightsizer' to re-enter the vCenter password if an analysis was running."
	;;
_create)
	create
	;;
uninstall)
	[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
	docker rm -f "$C" >/dev/null 2>&1 || true
	read -r -p "Also delete collected data and reports? [y/N] " a
	[[ "$a" =~ ^[Yy]$ ]] && docker volume rm rightsizer-data >/dev/null
	rm -f /usr/local/bin/rightsizer
	rm -rf /etc/rightsizer
	echo "rightsizer removed"
	;;
*)
	cat <<EOF
Usage: rightsizer [command]

  (none)     open the console
  status     one-line status
  export [F] save the latest PDF report to F
  logs       follow collector logs
  start|stop|restart
  update     pull the latest image and restart (data is kept)
  uninstall  remove rightsizer
EOF
	;;
esac
WRAPPER
chmod 0755 "$BIN"

info "Starting container"
"$BIN" _create

for _ in $(seq 1 20); do
	docker exec rightsizer /rightsizer status >/dev/null 2>&1 && break
	sleep 0.5
done
docker exec rightsizer /rightsizer status >/dev/null 2>&1 || die "container did not start, check: docker logs rightsizer"

echo
bold "rightsizer is running."
echo "  Open the console:   rightsizer"
echo "  Report downloads:   https://${HOST}:${PORT}/<link shown in the console>, only while a report is shared"
echo "  Help:               rightsizer help"
echo
echo "Create a read-only vCenter user for the analysis; rightsizer never changes your environment."
