#!/bin/sh
# Prints "release" for a tag like v1.4.0 and "pre" for a pre-release like
# v1.4.0-rc.1, -beta.2 or -alpha; exits 1 for anything else, so CI never
# publishes a tag that installs and update checks would refuse. install.sh
# and internal/version use the same rule.
set -eu
if printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo release
elif printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+-(rc|beta|alpha)\.?[0-9]*$'; then
	echo pre
else
	echo "unsupported tag $1: use vX.Y.Z, or vX.Y.Z-rc.N, -beta.N or -alpha.N for a pre-release" >&2
	exit 1
fi
