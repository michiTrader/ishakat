#!/bin/sh
# install.sh — fetch and install the right ishakat binary for this machine.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/michiTrader/ishakat/main/install.sh | sh
#
# What it does, in order: detect OS/arch (Termux counts as android/arm64),
# pick the matching release asset from the latest GitHub Release, download it,
# verify its checksum, and place it on PATH — $PREFIX/bin under Termux,
# otherwise the first writable directory among /usr/local/bin and
# ~/.local/bin. Idempotent: running it again just overwrites the binary.
#
# No sudo is used anywhere. On Termux there is no sudo binary at all, and
# $PREFIX/bin is already owned by the user, so requiring it would break the
# one platform this project cares about most.
#
# POSIX sh on purpose (no bashisms): Termux's default shell is bash, but
# minimal containers and some CI images only carry dash/ash, and this script
# has no feature that needs anything past POSIX.

set -eu

REPO="michiTrader/ishakat"
BIN_NAME="ishakat"
GITHUB="https://github.com/${REPO}"

log() { printf '%s\n' "$*" >&2; }
die() {
	log "install.sh: $*"
	exit 1
}

# require FILE checks that a command exists before it is used, so the failure
# names the missing tool instead of surfacing as a cryptic "command not
# found" three lines later inside a pipeline.
require() {
	command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found on PATH"
}

# detect_platform prints "<os>-<goarch>", matching the suffix release.yml
# gives each asset. Termux is detected the same way internal/xdg.IsTermux
# does in Go — via $PREFIX containing "com.termux", or the app's data
# directory existing — and always maps to android/arm64: Termux only ships
# on that architecture in practice, and a CGO-less android build is the one
# failure mode §3/§13bis calls out by name (starts, prints --version, then
# dies on the first real DNS lookup), so this path exists specifically to
# make sure Termux users get the NDK-built artifact and never the generic
# linux one.
detect_platform() {
	if [ -n "${PREFIX:-}" ] && case "$PREFIX" in *com.termux*) true ;; *) false ;; esac; then
		echo "android-arm64"
		return
	fi
	if [ -d /data/data/com.termux/files/usr ]; then
		echo "android-arm64"
		return
	fi

	os=$(uname -s)
	arch=$(uname -m)

	case "$os" in
	Linux) goos="linux" ;;
	Darwin) goos="darwin" ;;
	*) die "unsupported OS: $os (only Linux, Darwin and Termux/Android are built by release.yml)" ;;
	esac

	case "$arch" in
	x86_64 | amd64) goarch="amd64" ;;
	aarch64 | arm64) goarch="arm64" ;;
	*) die "unsupported architecture: $arch" ;;
	esac

	# darwin/amd64 and linux/arm64... only linux/arm64 and darwin/arm64 are
	# published today (§13bis's matrix); reject the combination early with a
	# clear message instead of a 404 further down.
	if [ "$goos" = "darwin" ] && [ "$goarch" = "amd64" ]; then
		die "darwin/amd64 is not published by release.yml yet — build from source with 'go build ./cmd/ishakat'"
	fi

	echo "${goos}-${goarch}"
}

# install_dir prints where the binary should land, in priority order:
# Termux's $PREFIX/bin (no sudo exists there and it is already on PATH),
# then /usr/local/bin if writable, then ~/.local/bin (creating it and
# warning if it is not already on PATH, since a silent install to a
# directory the shell never searches is worse than no install).
install_dir() {
	if [ -n "${PREFIX:-}" ] && [ -d "$PREFIX/bin" ]; then
		echo "$PREFIX/bin"
		return
	fi
	if [ -w /usr/local/bin ]; then
		echo /usr/local/bin
		return
	fi
	local_bin="${HOME}/.local/bin"
	mkdir -p "$local_bin"
	echo "$local_bin"
}

main() {
	require curl
	require uname
	require mktemp
	require sed

	platform=$(detect_platform)
	log "ishakat: detected platform ${platform}"

	# resolve the latest tag via the GitHub redirect instead of the API, so
	# an unauthenticated run never hits GitHub's low anonymous rate limit —
	# the endpoint used here is a plain redirect, not api.github.com. If no
	# release exists yet, GitHub serves /releases with no redirect at all,
	# so the URL comes back unchanged and needs its own check rather than
	# silently continuing with a URL that never had a tag in it.
	latest_url="${GITHUB}/releases/latest"
	resolved=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$latest_url")
	case "$resolved" in
	*/releases/tag/*) tag=$(printf '%s' "$resolved" | sed 's#.*/tag/##') ;;
	*) die "no published release found at ${latest_url} yet" ;;
	esac
	[ -n "$tag" ] || die "could not resolve the latest release tag from ${latest_url}"
	log "ishakat: latest release is ${tag}"

	asset="${BIN_NAME}-${platform}"
	case "$platform" in
	windows-*) asset="${asset}.exe" ;;
	esac

	download_url="${GITHUB}/releases/download/${tag}/${asset}"
	checksum_url="${GITHUB}/releases/download/${tag}/${asset}.sha256"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	log "ishakat: downloading ${asset}"
	curl -fsSL -o "${tmp}/${asset}" "$download_url" ||
		die "download failed: ${download_url} (does this release have a ${platform} asset?)"

	if curl -fsSL -o "${tmp}/${asset}.sha256" "$checksum_url" 2>/dev/null; then
		if command -v sha256sum >/dev/null 2>&1; then
			(cd "$tmp" && sha256sum -c "${asset}.sha256" >/dev/null) ||
				die "checksum verification failed for ${asset} — the download is corrupt or was tampered with"
			log "ishakat: checksum OK"
		elif command -v shasum >/dev/null 2>&1; then
			(cd "$tmp" && shasum -a 256 -c "${asset}.sha256" >/dev/null) ||
				die "checksum verification failed for ${asset} — the download is corrupt or was tampered with"
			log "ishakat: checksum OK"
		else
			log "ishakat: no sha256sum/shasum on this system, skipping verification"
		fi
	else
		log "ishakat: no checksum file published for this asset, skipping verification"
	fi

	dest_dir=$(install_dir)
	dest="${dest_dir}/${BIN_NAME}"
	chmod +x "${tmp}/${asset}"
	mv "${tmp}/${asset}" "$dest"

	log "ishakat: installed to ${dest}"

	case ":$PATH:" in
	*":${dest_dir}:"*) ;;
	*) log "ishakat: warning — ${dest_dir} is not on PATH, add it to your shell profile" ;;
	esac

	if command -v "$BIN_NAME" >/dev/null 2>&1; then
		log "ishakat: run 'ishakat doctor' to check the environment"
	else
		log "ishakat: run '${dest} doctor' to check the environment"
	fi
}

main "$@"
