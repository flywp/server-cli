#!/usr/bin/env bash
#
# Sign a published release: make sign-release VERSION=v0.2.1 KEY=<file or ->
#
# The signature says "this release is the code of my tag". So the script
# signs only what it can build again: it builds the release from your local
# tag, with the Go version of the CI build, and compares the binaries with
# the binaries that GitHub serves. A swapped archive or checksums.txt on
# GitHub, or a tag that moved on GitHub, stops the script before it signs.
#
# The binary holds the commit (vcs.revision), so equal binaries also prove
# that CI built the commit of your local tag. Review that commit before you
# sign: the signature is the approval.
#
# UPLOAD=0 does all checks and writes checksums.txt.sig, but does not upload it.
# SIGN_YES=1 skips the typed confirmation (tests only).

set -euo pipefail

version=${1:?usage: sign-release.sh <tag> <private key file, or - for stdin>}
key=${2:?usage: sign-release.sh <tag> <private key file, or - for stdin>}

root=$(git rev-parse --show-toplevel)
dir="$root/build/sign/$version"

if [ "$key" != "-" ]; then
	[ -f "$key" ] || { echo "No key file: $key" >&2; exit 1; }
	key="$(cd "$(dirname "$key")" && pwd)/$(basename "$key")"
fi

# The source of trust is your local tag, not the tag on GitHub.
if ! commit=$(git -C "$root" rev-parse --verify --quiet "refs/tags/$version^{commit}"); then
	echo "There is no local tag $version. Sign only a tag that you made or reviewed." >&2
	exit 1
fi

echo "Release $version is commit $(git -C "$root" log -1 --format='%h %s' "$commit")"
if [ "${SIGN_YES:-}" != "1" ]; then
	printf 'Type the tag to sign it: ' > /dev/tty
	read -r answer < /dev/tty
	[ "$answer" = "$version" ] || { echo "Not signed." >&2; exit 1; }
fi

rm -rf "$dir"
mkdir -p "$dir/ci" "$dir/ci-bin"
cleanup() { git -C "$root" worktree remove --force "$dir/src" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "Downloading the release from GitHub..."
gh release download "$version" --pattern 'fly-linux-*.tar.gz' --pattern checksums.txt --dir "$dir/ci"

# The archives agree with checksums.txt.
if command -v sha256sum >/dev/null 2>&1; then
	(cd "$dir/ci" && sha256sum -c checksums.txt)
else
	(cd "$dir/ci" && shasum -a 256 -c checksums.txt)
fi

# Build the release again from the local tag, with the Go version of CI.
git -C "$root" worktree add --detach --quiet "$dir/src" "$commit"
for archive in "$dir"/ci/fly-linux-*.tar.gz; do
	tar -xzf "$archive" -C "$dir/ci-bin"
done
goversions=$(for bin in "$dir"/ci-bin/*; do go version "$bin" | awk '{ print $NF }'; done | sort -u)
if [ "$(printf '%s\n' "$goversions" | wc -l)" -ne 1 ]; then
	echo "The CI binaries were built with different Go versions: $goversions" >&2
	exit 1
fi
echo "Building $version again with $goversions..."
(cd "$dir/src" && GOTOOLCHAIN="$goversions" make release VERSION="$version" >/dev/null)

# checksums.txt must name exactly the archives of the release build.
names() { sed 's/\r$//' "$1" | awk '{ sub(/^\*/, "", $2); print $2 }' | sort; }
if [ "$(names "$dir/ci/checksums.txt")" != "$(names "$dir/src/build/checksums.txt")" ]; then
	echo "checksums.txt on GitHub does not name the archives of the release build." >&2
	exit 1
fi

# The binaries are the same, byte for byte.
for bin in "$dir"/src/build/fly-linux-*; do
	case "$bin" in *.tar.gz) continue ;; esac
	name=$(basename "$bin")
	if ! cmp -s "$bin" "$dir/ci-bin/$name"; then
		echo "$name on GitHub is not the build of $version. Do not sign it." >&2
		exit 1
	fi
	echo "$name: the same as the build of the local tag"
done

# Sign and verify with the tool and the trusted keys of the tag: the keys
# that the agents of this release have.
(cd "$dir/src" && go run ./tools/releasesign sign -key "$key" -tag "$version" "$dir/ci/checksums.txt") > "$dir/checksums.txt.sig"
(cd "$dir/src" && go run ./tools/releasesign verify -tag "$version" "$dir/ci/checksums.txt" "$dir/checksums.txt.sig")

if [ "${UPLOAD:-1}" = "0" ]; then
	echo "Not uploaded (UPLOAD=0): $dir/checksums.txt.sig"
	exit 0
fi
gh release upload "$version" "$dir/checksums.txt.sig" --clobber
echo "Signed $version. Agents install it 24 hours after the signature."
