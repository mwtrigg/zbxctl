#!/bin/sh
# Prints the release notes for a tag from CHANGELOG.md: the section whose heading is the
# tag's version ("## [0.1.0]" for v0.1.0), or the [Unreleased] section when there is no
# such heading (the first release of a changelog that was kept under Unreleased). Fails
# if the section is empty, so a release never goes out with notes that say nothing.
#
#   scripts/release-notes.sh v0.1.0-rc.1
set -eu
tag="${1:?usage: release-notes.sh <tag>}"
version="${tag#v}"
file="$(dirname "$0")/../CHANGELOG.md"

section() { # print the body of the section with this heading
  awk -v h="## [$1]" '
    $0 == h || index($0, h " ") == 1 { on = 1; next }
    on && /^## \[/ { exit }
    on { print }
  ' "$file"
}

notes="$(section "$version")"
[ -n "$(printf '%s' "$notes" | tr -d '[:space:]')" ] || notes="$(section Unreleased)"
if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
  echo "release-notes.sh: CHANGELOG.md has no notes for $tag (no [$version] or [Unreleased] section with content)" >&2
  exit 1
fi
printf '%s\n' "$notes"
