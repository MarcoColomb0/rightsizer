#!/bin/sh
# Prints the notes of a release from CHANGELOG.md: the section under
# "## <tag> - <date>", without blank lines at either end. A pre-release such
# as v1.4.0-rc.1 without its own section uses "## Unreleased". Exits 1 when
# the section is missing or empty, so a release never ships without notes.
set -eu
tag="$1"
section() {
	awk -v tag="$1" '
		/^## / { if (found) exit; found = ($2 == tag); next }
		found
	' "${2:-CHANGELOG.md}" | sed -e '/./,$!d'
}
notes="$(section "$tag" "${2:-CHANGELOG.md}")"
if [ -z "$notes" ] && [ "$(sh "$(dirname "$0")/tag-kind.sh" "$tag" 2>/dev/null)" = pre ]; then
	notes="$(section Unreleased "${2:-CHANGELOG.md}")"
fi
if [ -z "$notes" ]; then
	echo "CHANGELOG.md has no notes for $tag: add a \"## $tag - YYYY-MM-DD\" section" >&2
	exit 1
fi
printf '%s\n' "$notes"
