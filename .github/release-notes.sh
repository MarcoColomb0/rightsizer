#!/bin/sh
# Prints the notes of a release from CHANGELOG.md: the section under
# "## <tag> - <date>", without blank lines at either end. Exits 1 when the
# section is missing or empty, so a release never ships without notes.
set -eu
tag="$1"
notes="$(awk -v tag="$tag" '
	/^## / { if (found) exit; found = ($2 == tag); next }
	found
' "${2:-CHANGELOG.md}" | sed -e '/./,$!d')"
if [ -z "$notes" ]; then
	echo "CHANGELOG.md has no notes for $tag: add a \"## $tag - YYYY-MM-DD\" section" >&2
	exit 1
fi
printf '%s\n' "$notes"
