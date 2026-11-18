#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Usage: global-json.sh FILE
#
# Rewrites Prowlarr's global.json at FILE so that the SDK it names is a
# floor rather than the one feature band allowed: upstream's sdk.version
# stays, with "rollForward": "latestFeature", which takes any later band of
# the same major and minor. Upstream sets no policy, which accepts its own
# band alone, so once Renovate moved the SDK image to a later band the
# build would refuse to start, and could deadlock with a Prowlarr bump.
# This is the one change the image's build makes to an upstream file, and
# it never leaves the backend stage.
#
# The Containerfile's backend stage runs it over its checkout's
# global.json. test/globaljson runs it over fixtures, and sees each way it
# can refuse refusing.
#
# Exit status: 0 when FILE is rewritten, printing what it now holds; 1
# when FILE has no sdk.version, or one that is not an SDK version, with an
# ERROR line on stderr and FILE as it was; 2 when FILE is not one regular
# file.
set -euo pipefail

if (($# != 1)) || [[ ! -f "${1}" ]]; then
	echo "usage: ${0} FILE, where FILE is Prowlarr's global.json" >&2
	exit 2
fi
FILE="${1}"
json=$(<"${FILE}")

# sdk.version, a JSON string. The sdk object holds no object of its own,
# so the first closing brace after its opening one ends it.
re='"sdk"[[:space:]]*:[[:space:]]*[{][^}]*"version"[[:space:]]*:[[:space:]]*"([^"]*)"'
if [[ ! "${json}" =~ ${re} ]]; then
	echo "ERROR: ${FILE} has no sdk.version to keep as the floor." >&2
	exit 1
fi
version="${BASH_REMATCH[1]}"
# A release's version, or a preview's, such as 11.0.100-rc.2.25502.107
if [[ ! "${version}" =~ ^[0-9]+[.][0-9]+[.][0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
	echo "ERROR: ${FILE}'s sdk.version is '${version}', not an SDK version." >&2
	exit 1
fi
printf '{"sdk":{"version":"%s","rollForward":"latestFeature"}}\n' "${version}" >"${FILE}"
cat "${FILE}"
