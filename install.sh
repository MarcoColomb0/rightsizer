#!/usr/bin/env bash
# rightsizer installer - https://github.com/MarcoColomb0/rightsizer
set -euo pipefail

REPO="MarcoColomb0/rightsizer"
CONF_DIR=/etc/rightsizer
CONF="${CONF_DIR}/rightsizer.env"
BIN=/usr/local/bin/rightsizer

PORT=8443 BIND=0.0.0.0 HOST="" TZ_NAME="" UPDATE_CHECK=true
# shellcheck disable=SC1090
[ -r "$CONF" ] && . "$CONF"
PORT="${RIGHTSIZER_PORT:-$PORT}"
BIND="${RIGHTSIZER_BIND:-$BIND}"
HOST="${RIGHTSIZER_HOST:-$HOST}"
TAG="${RIGHTSIZER_TAG:-latest}"
BUILD=0
LAUNCHER_ONLY=0

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '\033[36m›\033[0m %s\n' "$*"; }
die() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<EOF
Usage: install.sh [options]

  --port N           HTTPS port for report downloads (default 8443)
  --bind ADDR        address the download port binds to (default 0.0.0.0)
  --host NAME        host name or IP shown in download links (default: primary IP)
  --tag vX.Y.Z       release to install (default: latest release)
  --build            build the image from source instead of pulling it
  --no-update-check  never look for new releases
  -h, --help         show this help
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--port) PORT="${2:?}"; shift 2 ;;
	--bind) BIND="${2:?}"; shift 2 ;;
	--host) HOST="${2:?}"; shift 2 ;;
	--tag) TAG="${2:?}"; shift 2 ;;
	--build) BUILD=1; shift ;;
	--no-update-check) UPDATE_CHECK=false; shift ;;
	--launcher-only) LAUNCHER_ONLY=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage; die "unknown option: $1" ;;
	esac
done

[ "$(uname -s)" = "Linux" ] || die "rightsizer supports Linux hosts only."
[ "$(id -u)" -eq 0 ] || die "run as root, e.g.: curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | sudo bash"
command -v docker >/dev/null 2>&1 || die "Docker is required. Install it first: https://docs.docker.com/engine/install/"
docker info >/dev/null 2>&1 || die "Docker is installed but the daemon is not running (try: systemctl start docker)."
command -v curl >/dev/null 2>&1 || die "curl is required."

if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
	die "invalid port: $PORT"
fi
if [ -z "$HOST" ]; then
	HOST="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')"
	[ -n "$HOST" ] || HOST="$(hostname -I 2>/dev/null | awk '{print $1}')"
	[ -n "$HOST" ] || HOST="$(hostname)"
fi
[[ "$HOST" =~ ^[A-Za-z0-9.:-]+$ ]] || die "invalid host: $HOST"
[[ "$BIND" =~ ^[0-9A-Fa-f.:]+$ ]] || die "invalid bind address: $BIND"
if [ -z "$TZ_NAME" ]; then
	TZ_NAME="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
	[ -n "$TZ_NAME" ] || TZ_NAME="$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || true)"
fi
[[ "$TZ_NAME" =~ ^[A-Za-z0-9/_+-]+$ ]] || TZ_NAME=UTC

if [ "$TAG" = "latest" ]; then
	TAG="$(curl -fsS --proto '=https' --max-time 10 -H 'Accept: application/vnd.github+json' \
		"https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1 || true)"
	if [ -z "$TAG" ]; then
		info "No published release yet, building the development version from source"
		TAG=main
		BUILD=1
	fi
fi
[[ "$TAG" =~ ^(v[0-9]+\.[0-9]+\.[0-9]+|main)$ ]] || die "invalid tag: $TAG"

bold "Installing rightsizer ${TAG}"

install -d -m 0755 "$CONF_DIR"
umask 022
cat >"$CONF" <<EOF
PORT=${PORT}
BIND=${BIND}
HOST=${HOST}
TZ_NAME=${TZ_NAME}
UPDATE_CHECK=${UPDATE_CHECK}
EOF

cat >"${BIN}.new" <<'WRAPPER'
#!/usr/bin/env bash
# rightsizer launcher - https://github.com/MarcoColomb0/rightsizer
set -euo pipefail

LAUNCHER_VERSION=__LAUNCHER_VERSION__
REPO=MarcoColomb0/rightsizer
REGISTRY=ghcr.io/marcocolomb0/rightsizer
C=rightsizer
DATA_VOL=rightsizer-data
BACKUP_VOL=rightsizer-backups
CONF=/etc/rightsizer/rightsizer.env
EXIT_UPGRADE=42

[ -r "$CONF" ] || { echo "rightsizer: $CONF missing, re-run the installer" >&2; exit 1; }
# shellcheck disable=SC1090
. "$CONF"

say() { printf '\033[36m›\033[0m %s\n' "$*"; }
ok() { printf '\033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[33m!\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }
step() { printf '\n\033[36m[%s/6]\033[0m \033[1m%s\033[0m\n' "$1" "$2"; }
fetch() { curl -fsSL --proto '=https' --tlsv1.2 --max-time 60 "$@"; }

ask() {
	local a
	printf '%s \033[36m[Y/n]\033[0m ' "$1"
	read -r a </dev/tty || a=n
	[[ -z "$a" || "$a" =~ ^[Yy] ]]
}

sandbox=(--rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true --user 65532:65532 --entrypoint /rightsizer)

create() {
	docker volume create "$DATA_VOL" >/dev/null
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
		-v "$DATA_VOL":/data \
		--log-opt max-size=10m --log-opt max-file=3 \
		"$1" daemon >/dev/null
}

running() { [ "$(docker inspect -f '{{.State.Running}}' "$C" 2>/dev/null)" = "true" ]; }
installed_version() { docker exec "$C" /rightsizer version 2>/dev/null | awk '{print $2}'; }
phase() { docker exec "$C" /rightsizer status 2>/dev/null | cut -d: -f1; }

wait_ready() {
	for _ in $(seq 1 60); do
		docker exec "$C" /rightsizer status >/dev/null 2>&1 && return 0
		running || return 1
		printf '.'
		sleep 1
	done
	return 1
}

newer() {
	local a="${1#v}" b="${2#v}"
	[[ "$a" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$b" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
	[ "$a" != "$b" ] && [ "$(printf '%s\n%s\n' "$a" "$b" | sort -V | tail -n1)" = "$a" ]
}

LATEST="" LATEST_URL=""
latest_release() {
	if [ "${UPDATE_CHECK:-true}" != "true" ] || [ -n "${RIGHTSIZER_NO_UPDATE_CHECK:-}" ]; then
		return 0
	fi
	command -v curl >/dev/null 2>&1 || return 0
	local dir="${XDG_CACHE_HOME:-$HOME/.cache}/rightsizer" cache body
	cache="$dir/latest"
	if [ "${1:-}" != "force" ] && [ -f "$cache" ] && [ -z "$(find "$cache" -mmin +360 2>/dev/null)" ]; then
		read -r LATEST LATEST_URL <"$cache" || true
	else
		body="$(curl -fsS --proto '=https' --max-time 5 -H 'Accept: application/vnd.github+json' \
			"https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)"
		LATEST="$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' <<<"$body" | head -n1)"
		LATEST_URL="https://github.com/${REPO}/releases/tag/${LATEST}"
		if [[ "$LATEST" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
			{ mkdir -p "$dir" && printf '%s %s\n' "$LATEST" "$LATEST_URL" >"$cache"; } 2>/dev/null || true
		fi
	fi
	[[ "$LATEST" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { LATEST="" LATEST_URL=""; }
}

can_attest() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

update_launcher() {
	local tag="$1" tmp
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' RETURN
	say "Downloading launcher ${tag}"
	fetch -o "$tmp/install.sh" "https://github.com/${REPO}/releases/download/${tag}/install.sh" || die "download failed"
	fetch -o "$tmp/SHA256SUMS" "https://github.com/${REPO}/releases/download/${tag}/SHA256SUMS" || die "download failed"
	(cd "$tmp" && grep -E '  install\.sh$' SHA256SUMS | sha256sum -c --quiet -) || die "checksum mismatch, launcher not updated"
	if can_attest; then
		gh attestation verify "$tmp/install.sh" --repo "$REPO" >/dev/null || die "provenance check failed, launcher not updated"
		ok "Checksum and build provenance verified"
	else
		ok "Checksum verified (install and log in to the GitHub CLI to also verify build provenance)"
	fi
	local sudo=()
	[ "$(id -u)" -eq 0 ] || sudo=(sudo)
	"${sudo[@]}" bash "$tmp/install.sh" --tag "$tag" --launcher-only
}

upgrade() {
	local tag="$1" old_image old_id old_ver old_phase new_image file="" new_phase
	[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid release: $tag"
	docker inspect "$C" >/dev/null 2>&1 || die "rightsizer container not found, re-run the installer"
	old_image="$(docker inspect -f '{{.Config.Image}}' "$C")"
	old_id="$(docker inspect -f '{{.Image}}' "$C")"
	running || docker start "$C" >/dev/null
	wait_ready >/dev/null || true
	old_ver="$(installed_version || true)"
	old_ver="${old_ver:-unknown}"
	old_phase="$(phase || true)"
	old_phase="${old_phase:-unknown}"
	docker rm -f "$C-previous" >/dev/null 2>&1 || true

	rollback() {
		trap - INT TERM
		printf '\n'
		warn "$1. Rolling back to ${old_ver}."
		docker rm -f "$C" >/dev/null 2>&1 || true
		if [ -n "$file" ]; then
			docker run "${sandbox[@]}" -v "$DATA_VOL":/data -v "$BACKUP_VOL":/backup:ro "$old_id" restore "/backup/$file" >/dev/null 2>&1 ||
				docker run "${sandbox[@]}" -v "$DATA_VOL":/data -v "$BACKUP_VOL":/backup:ro "$new_image" restore "/backup/$file" >/dev/null ||
				die "automatic restore failed; backup kept in volume ${BACKUP_VOL}: ${file}"
			ok "Data restored from ${file}"
		fi
		if docker inspect "$C-previous" >/dev/null 2>&1; then
			docker rename "$C-previous" "$C"
			docker start "$C" >/dev/null
		fi
		die "Upgrade failed, ${old_ver} is running again with its data."
	}

	if [[ "$old_image" == rightsizer:* ]]; then
		new_image="rightsizer:${tag}"
		step 1 "Building ${new_image} from source"
		DOCKER_BUILDKIT=1 docker build --progress=plain --build-arg "VERSION=${tag}" -t "$new_image" \
			"https://github.com/${REPO}.git#${tag}" 2>&1 | grep --line-buffered -E '^#[0-9]+ \[|DONE|ERROR' || true
		docker image inspect "$new_image" >/dev/null 2>&1 || die "build failed, nothing was changed"
	else
		new_image="${REGISTRY}:${tag#v}"
		step 1 "Downloading ${new_image}"
		docker pull "$new_image" || die "download failed, nothing was changed"
	fi

	step 2 "Verifying image"
	if [[ "$new_image" == rightsizer:* ]]; then
		ok "Built locally from tag ${tag}"
	elif can_attest; then
		gh attestation verify "oci://${new_image}" --repo "$REPO" >/dev/null || die "provenance check failed, nothing was changed"
		ok "Build provenance verified"
	else
		ok "Pulled over TLS from ${REGISTRY} (log in to the GitHub CLI to also verify build provenance)"
	fi

	step 3 "Stopping collector and saving state"
	trap 'rollback "Interrupted"' INT TERM
	docker stop -t 60 "$C" >/dev/null || rollback "Could not stop the collector"
	docker rename "$C" "$C-previous" || rollback "Could not rename the old container"
	ok "State saved (was: ${old_phase})"

	step 4 "Backing up data"
	docker volume create "$BACKUP_VOL" >/dev/null
	file="rightsizer-data-$(date +%Y%m%d-%H%M%S)-${old_ver}.tar.gz"
	docker run "${sandbox[@]}" -v "$DATA_VOL":/data:ro -v "$BACKUP_VOL":/backup "$new_image" backup "/backup/$file" ||
		{ file=""; rollback "Backup failed"; }
	ok "Saved ${file} in volume ${BACKUP_VOL} (last 3 kept)"

	step 5 "Starting ${tag}"
	create "$new_image" || rollback "Container did not start"
	ok "Container started"

	step 6 "Checking health and data"
	wait_ready || rollback "New version did not become ready"
	printf '\n'
	new_phase="$(phase || true)"
	new_phase="${new_phase:-unknown}"
	if [ "$old_phase" != "idle" ] && [ "$old_phase" != "unknown" ] && [ "$new_phase" = "idle" ]; then
		rollback "New version did not load the saved analysis"
	fi
	ok "Collector ready (now: ${new_phase})"

	trap - INT TERM
	docker rm "$C-previous" >/dev/null 2>&1 || true
	[ "$old_id" = "$(docker inspect -f '{{.Image}}' "$C")" ] || docker image rm "$old_id" >/dev/null 2>&1 || true
	printf '\n'
	ok "Upgraded ${old_ver} → ${tag}. Collected data kept."
	if [ "$new_phase" = "paused" ]; then
		say "Enter the vCenter password in the console to continue the analysis."
	fi
	return 0
}

console() {
	docker inspect "$C" >/dev/null 2>&1 || die "rightsizer container not found, re-run the installer"
	if ! running; then
		ask "rightsizer is stopped. Start it?" || exit 0
		docker start "$C" >/dev/null
		wait_ready >/dev/null || die "rightsizer did not start, check: rightsizer logs"
	fi

	latest_release
	if [ -n "$LATEST" ] && [ -z "${RIGHTSIZER_LAUNCHER_CHECKED:-}" ] && newer "$LATEST" "$LAUNCHER_VERSION"; then
		printf '\033[1mLauncher update available:\033[0m %s → %s\n  %s\n' "$LAUNCHER_VERSION" "$LATEST" "$LATEST_URL"
		if ask "Update the launcher now?"; then
			update_launcher "$LATEST"
			RIGHTSIZER_LAUNCHER_CHECKED=1 exec /usr/local/bin/rightsizer "$@"
		fi
	fi

	local rc offer="$LATEST"
	while :; do
		set +e
		docker exec -it \
			-e TERM="${TERM:-xterm-256color}" -e COLORTERM="${COLORTERM:-}" \
			-e RIGHTSIZER_LATEST="$offer" -e RIGHTSIZER_RELEASE_URL="$LATEST_URL" \
			"$C" /rightsizer tui
		rc=$?
		set -e
		[ "$rc" -eq "$EXIT_UPGRADE" ] || exit "$rc"
		printf '\033[1mUpgrading rightsizer to %s\033[0m\n' "$offer"
		upgrade "$offer"
		offer=""
		say "Reopening the console…"
		sleep 2
	done
}

case "${1:-tui}" in
tui) console "$@" ;;
status | version)
	[ "$1" = "version" ] && echo "launcher ${LAUNCHER_VERSION}"
	docker exec "$C" /rightsizer "$1"
	;;
export)
	out="${2:-rightsizer-report-$(date +%Y%m%d-%H%M).pdf}"
	docker exec "$C" /rightsizer export >"$out.tmp" && mv "$out.tmp" "$out" && chmod 600 "$out" && echo "saved $out"
	;;
backup)
	docker volume create "$BACKUP_VOL" >/dev/null
	f="rightsizer-data-$(date +%Y%m%d-%H%M%S)-manual.tar.gz"
	docker run "${sandbox[@]}" -v "$DATA_VOL":/data:ro -v "$BACKUP_VOL":/backup \
		"$(docker inspect -f '{{.Image}}' "$C")" backup "/backup/$f"
	;;
logs) docker logs -f --tail 200 "$C" ;;
start | stop | restart) docker "$1" "$C" >/dev/null && echo "rightsizer ${1}ed" ;;
update)
	latest_release force
	[ -n "$LATEST" ] || die "could not find a published release"
	if newer "$LATEST" "$LAUNCHER_VERSION" && [ -z "${RIGHTSIZER_LAUNCHER_CHECKED:-}" ]; then
		update_launcher "$LATEST"
		RIGHTSIZER_LAUNCHER_CHECKED=1 exec /usr/local/bin/rightsizer update
	fi
	cur="$(installed_version || true)"
	if newer "$LATEST" "${cur:-unknown}"; then
		upgrade "$LATEST"
	else
		ok "Already up to date (${cur:-unknown})"
	fi
	;;
_create) create "$2" ;;
_upgrade) upgrade "$2" ;;
uninstall)
	[ "$(id -u)" -eq 0 ] || die "run as root"
	docker rm -f "$C" "$C-previous" >/dev/null 2>&1 || true
	if ask "Also delete collected data, reports and backups?"; then
		docker volume rm "$DATA_VOL" "$BACKUP_VOL" >/dev/null 2>&1 || true
	fi
	rm -f /usr/local/bin/rightsizer
	rm -rf /etc/rightsizer
	echo "rightsizer removed"
	;;
*)
	cat <<EOF
Usage: rightsizer [command]

  (none)      open the console (offers updates when available)
  status      one-line status
  export [F]  save the latest PDF report to F
  backup      back up collected data to the ${BACKUP_VOL} volume
  update      update launcher and container to the latest release
  logs        follow collector logs
  start|stop|restart
  version
  uninstall   remove rightsizer
EOF
	;;
esac
WRAPPER
sed -i "s/__LAUNCHER_VERSION__/${TAG}/" "${BIN}.new"
chmod 0755 "${BIN}.new"
mv "${BIN}.new" "$BIN"

if [ "$LAUNCHER_ONLY" -eq 1 ]; then
	echo "Launcher updated to ${TAG}."
	exit 0
fi

if [ "$BUILD" -eq 1 ]; then
	IMAGE="rightsizer:${TAG}"
	VERSION="$TAG"
	[ "$TAG" = "main" ] && VERSION=dev
	info "Building ${IMAGE} from source"
	DOCKER_BUILDKIT=1 docker build -q --build-arg "VERSION=${VERSION}" -t "$IMAGE" "https://github.com/${REPO}.git#${TAG}" >/dev/null
else
	IMAGE="ghcr.io/marcocolomb0/rightsizer:${TAG#v}"
fi

if docker inspect rightsizer >/dev/null 2>&1; then
	current="$(docker inspect -f '{{.Config.Image}}' rightsizer)"
	if [ "$current" = "$IMAGE" ] && [ "$TAG" != "main" ]; then
		info "Container already runs ${TAG}"
	elif [ "$TAG" = "main" ]; then
		die "rightsizer is already installed; development builds are not upgraded in place. Use 'rightsizer update' once a release exists."
	else
		info "Existing installation found, upgrading with backup"
		"$BIN" _upgrade "$TAG"
	fi
else
	if [ "$BUILD" -eq 0 ]; then
		info "Pulling ${IMAGE}"
		docker pull "$IMAGE"
	fi
	info "Starting container"
	"$BIN" _create "$IMAGE"
	for _ in $(seq 1 20); do
		docker exec rightsizer /rightsizer status >/dev/null 2>&1 && break
		sleep 0.5
	done
	docker exec rightsizer /rightsizer status >/dev/null 2>&1 || die "container did not start, check: docker logs rightsizer"
fi

echo
bold "rightsizer ${TAG} is running."
echo "  Open the console:   rightsizer"
echo "  Report downloads:   https://${HOST}:${PORT}/<link shown in the console>, only while a report is shared"
echo "  Help:               rightsizer help"
echo
echo "Create a read-only vCenter user for the analysis; rightsizer never changes your environment."
