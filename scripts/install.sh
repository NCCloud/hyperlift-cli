#!/bin/sh
# install.sh — download and install the hyperlift CLI from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/nccloud/hyperlift-cli/main/scripts/install.sh | sh
#
# Environment overrides:
#   HYPERLIFT_VERSION   tag to install (e.g. v1.2.3). Default: latest release.
#   HYPERLIFT_INSTALL   install directory.                Default: /usr/local/bin
#
# This downloads the archive matching your OS/arch, verifies its SHA-256 against
# the release's SHA256SUMS asset, and installs the `hyperlift` binary.
#
# POSIX sh only — no bashisms.

set -eu

REPO="nccloud/hyperlift-cli"
BINARY="hyperlift"
INSTALL_DIR="${HYPERLIFT_INSTALL:-/usr/local/bin}"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

err() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

info() {
  printf '%s\n' "$1" >&2
}

have() {
  command -v "$1" >/dev/null 2>&1
}

# ---------------------------------------------------------------------------
# detect OS / arch (matching GoReleaser's {{ .Os }}_{{ .Arch }} tokens)
# ---------------------------------------------------------------------------

detect_os() {
  os="$(uname -s)"
  case "$os" in
    Linux) echo "linux" ;;
    Darwin) echo "darwin" ;;
    *) err "unsupported operating system: $os (use the Windows .zip from the Releases page)" ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64 | amd64) echo "amd64" ;;
    aarch64 | arm64) echo "arm64" ;;
    *) err "unsupported architecture: $arch" ;;
  esac
}

# ---------------------------------------------------------------------------
# download primitives
# ---------------------------------------------------------------------------

# download URL OUTPUT_FILE  (an OUTPUT_FILE of "-" prints to stdout)
download() {
  url="$1"
  out="$2"
  if have curl; then
    curl -fsSL -o "$out" "$url"
  elif have wget; then
    wget -q -O "$out" "$url"
  else
    err "need curl or wget to download files"
  fi
}

# resolve_latest_tag -> prints the latest release tag (e.g. v1.2.3)
resolve_latest_tag() {
  api="https://api.github.com/repos/${REPO}/releases/latest"
  # Extract "tag_name": "vX.Y.Z" without depending on jq.
  body="$(download "$api" -)" || err "failed to query the latest release (GitHub API unreachable or rate limited; set HYPERLIFT_VERSION to a release tag to skip the API call)"
  tag="$(printf '%s' "$body" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$tag" ] || err "could not determine the latest release tag (no releases yet?)"
  printf '%s' "$tag"
}

# sha256_of FILE -> prints the lowercase hex digest
sha256_of() {
  file="$1"
  if have sha256sum; then
    sha256sum "$file" | awk '{print $1}'
  elif have shasum; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    err "need sha256sum or shasum to verify the download"
  fi
}

# http_status URL -> prints the HTTP status code of a HEAD request
http_status() {
  url="$1"
  if have curl; then
    curl -sIL -o /dev/null -w '%{http_code}' "$url" || printf '000'
  else
    # wget has no portable status probe; 000 means "could not tell".
    if wget -q --spider "$url" 2>/dev/null; then printf '200'; else printf '000'; fi
  fi
}

# verify_signature BASE_URL TMPDIR
#
# Cosign keyless verification of SHA256SUMS before we trust it. GoReleaser
# signs SHA256SUMS with cosign (Sigstore OIDC) and publishes one extra asset:
# SHA256SUMS.sigstore.json (the verification bundle).
#
# Skips only in two safe cases: cosign is not installed, or the release has
# no bundle asset (HTTP 404 — an unsigned/legacy release). Every other
# download failure is fatal: a transport error must not silently downgrade
# a signed release to checksum-only verification.
verify_signature() {
  base="$1"
  tmp="$2"

  if ! have cosign; then
    info "cosign not found; skipping signature verification (SHA-256 checksum still enforced)."
    return 0
  fi

  bundle_url="${base}/SHA256SUMS.sigstore.json"
  if ! download "$bundle_url" "${tmp}/SHA256SUMS.sigstore.json" 2>/dev/null; then
    if [ "$(http_status "$bundle_url")" = "404" ]; then
      info "no cosign bundle published for this release; skipping signature verification."
      return 0
    fi
    err "failed to download ${bundle_url} (refusing to skip verification on a transport error)"
  fi

  info "Verifying SHA256SUMS signature with cosign..."
  # Keyless verification: the signing identity must be this repo's release
  # workflow, and the OIDC issuer must be GitHub Actions.
  cosign verify-blob \
    --bundle "${tmp}/SHA256SUMS.sigstore.json" \
    --certificate-identity-regexp "https://github.com/${REPO}/.github/workflows/release.yaml@.*" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "${tmp}/SHA256SUMS" >/dev/null \
    || err "cosign signature verification failed for SHA256SUMS (refusing to install)"
  info "Signature OK."
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

main() {
  os="$(detect_os)"
  arch="$(detect_arch)"

  tag="${HYPERLIFT_VERSION:-}"
  if [ -z "$tag" ]; then
    info "Resolving latest release..."
    tag="$(resolve_latest_tag)"
  fi
  info "Installing ${BINARY} ${tag} for ${os}/${arch}..."

  # Asset naming is a contract with .goreleaser.yaml:
  #   hyperlift_<os>_<arch>.tar.gz  and  SHA256SUMS
  archive="${BINARY}_${os}_${arch}.tar.gz"
  base="https://github.com/${REPO}/releases/download/${tag}"
  archive_url="${base}/${archive}"
  sums_url="${base}/SHA256SUMS"

  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hyperlift-install.XXXXXX")" \
    || err "could not create a temp directory"
  cleanup() { rm -rf "$tmp"; }
  trap cleanup EXIT
  trap 'exit 1' HUP INT TERM

  info "Downloading ${archive}..."
  download "$archive_url" "${tmp}/${archive}" \
    || err "failed to download ${archive_url}"

  info "Downloading SHA256SUMS..."
  download "$sums_url" "${tmp}/SHA256SUMS" \
    || err "failed to download ${sums_url}"

  verify_signature "$base" "$tmp"

  info "Verifying checksum..."
  # sha256sum format: "<hex>  <name>", with "*" marking a binary-mode file.
  want="$(grep "[ *]${archive}\$" "${tmp}/SHA256SUMS" | awk '{print $1}')"
  [ -n "$want" ] || err "no checksum for ${archive} in SHA256SUMS"
  got="$(sha256_of "${tmp}/${archive}")"
  if [ "$want" != "$got" ]; then
    err "checksum mismatch for ${archive}: expected ${want}, got ${got}"
  fi
  info "Checksum OK."

  info "Extracting..."
  tar -xzf "${tmp}/${archive}" -C "$tmp" \
    || err "failed to extract ${archive}"
  [ -f "${tmp}/${BINARY}" ] || err "archive did not contain a ${BINARY} binary"
  chmod +x "${tmp}/${BINARY}"

  # Install with install(1): unlike mv, it gives the destination fresh
  # ownership (root when elevated) and 0755, never the temp file's.
  dest="${INSTALL_DIR}/${BINARY}"
  [ -d "$dest" ] && err "${dest} is a directory; remove it or set HYPERLIFT_INSTALL elsewhere"
  if [ -w "$INSTALL_DIR" ] || { [ ! -e "$INSTALL_DIR" ] && mkdir -p "$INSTALL_DIR" 2>/dev/null; }; then
    install -m 0755 "${tmp}/${BINARY}" "$dest"
  elif have sudo; then
    info "Elevating with sudo to install into ${INSTALL_DIR}..."
    sudo mkdir -p "$INSTALL_DIR"
    sudo install -m 0755 "${tmp}/${BINARY}" "$dest"
  else
    err "cannot write to ${INSTALL_DIR} and sudo is unavailable; set HYPERLIFT_INSTALL to a writable dir"
  fi

  info "Installed ${BINARY} to ${dest}"
  info ""
  info "Run '${BINARY} version' to verify, then '${BINARY} auth login' to get started."
}

main "$@"
