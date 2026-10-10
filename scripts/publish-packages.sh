#!/usr/bin/env bash
# Publish a skimmer release to the Homebrew tap (dorkitude/homebrew-tap) and the
# Scoop bucket (dorkitude/scoop-bucket).
#
# Run after the GitHub release for the tag exists (the release workflow builds
# it). Needs `gh` logged in with push access to both repos. Idempotent: if the
# formula and manifest already match the tag, nothing is committed.
#
#   scripts/publish-packages.sh v0.1.100
#   DRY_RUN=1 scripts/publish-packages.sh v0.1.100   # print, don't push
set -euo pipefail

TAG="${1:?usage: $0 vX.Y.Z}"
VERSION="${TAG#v}"
REPO="dorkitude/skimmer"
DESC="Skim pages, files, and lists with Jev: keep only what helps with your goal. Saves agent tokens."
HOMEPAGE="https://github.com/${REPO}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> Fetching release assets for ${TAG}"
gh release download "$TAG" -R "$REPO" -p checksums.txt -D "$WORK"
sha_of() { awk -v f="$1" '$2 == f {print $1}' "$WORK/checksums.txt"; }
WIN_AMD64="skimmer_${VERSION}_windows_amd64.zip"
WIN_ARM64="skimmer_${VERSION}_windows_arm64.zip"
WIN_AMD64_SHA="$(sha_of "$WIN_AMD64")"
WIN_ARM64_SHA="$(sha_of "$WIN_ARM64")"
[ -n "$WIN_AMD64_SHA" ] && [ -n "$WIN_ARM64_SHA" ] || { echo "Windows zips missing from checksums.txt" >&2; exit 1; }

echo "==> Hashing the source tarball"
curl -fsSL "https://github.com/${REPO}/archive/refs/tags/${TAG}.tar.gz" -o "$WORK/src.tar.gz"
SRC_SHA="$(sha256sum "$WORK/src.tar.gz" | awk '{print $1}')"

publish() { # repo, path, file, message
  local repo="$1" path="$2" src="$3" msg="$4" dir="$WORK/$(basename "$1")"
  gh repo clone "$repo" "$dir" -- -q --depth 1
  mkdir -p "$(dirname "$dir/$path")"
  cp "$src" "$dir/$path"
  if git -C "$dir" diff --quiet -- "$path" && git -C "$dir" ls-files --error-unmatch "$path" >/dev/null 2>&1; then
    echo "    $repo: $path already up to date"
    return
  fi
  if [ -n "${DRY_RUN:-}" ]; then
    echo "    [dry run] would commit $path to $repo:"; cat "$src"; return
  fi
  git -C "$dir" add "$path"
  git -C "$dir" commit -q -m "$msg"
  git -C "$dir" push -q origin HEAD
  echo "    $repo: pushed $path"
}

echo "==> Homebrew formula"
cat > "$WORK/skimmer.rb" <<EOF
class Skimmer < Formula
  desc "${DESC}"
  homepage "${HOMEPAGE}"
  url "https://github.com/${REPO}/archive/refs/tags/${TAG}.tar.gz"
  sha256 "${SRC_SHA}"
  license "MIT"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X github.com/dorkitude/skimmer/cmd.Version=#{version}"), "."
    generate_completions_from_executable(bin/"skimmer", "completion")
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/skimmer --version")
    assert_match "skim_document", shell_output("#{bin}/skimmer prompts list")
  end
end
EOF
publish dorkitude/homebrew-tap Formula/skimmer.rb "$WORK/skimmer.rb" "skimmer ${VERSION}"

echo "==> Scoop manifest"
cat > "$WORK/skimmer.json" <<EOF
{
  "version": "${VERSION}",
  "description": "${DESC}",
  "homepage": "${HOMEPAGE}",
  "license": "MIT",
  "architecture": {
    "64bit": {
      "url": "https://github.com/${REPO}/releases/download/${TAG}/${WIN_AMD64}",
      "hash": "${WIN_AMD64_SHA}",
      "bin": "skimmer.exe"
    },
    "arm64": {
      "url": "https://github.com/${REPO}/releases/download/${TAG}/${WIN_ARM64}",
      "hash": "${WIN_ARM64_SHA}",
      "bin": "skimmer.exe"
    }
  },
  "checkver": {
    "github": "${HOMEPAGE}"
  },
  "autoupdate": {
    "architecture": {
      "64bit": {
        "url": "https://github.com/${REPO}/releases/download/v\$version/skimmer_\$version_windows_amd64.zip"
      },
      "arm64": {
        "url": "https://github.com/${REPO}/releases/download/v\$version/skimmer_\$version_windows_arm64.zip"
      }
    }
  }
}
EOF
python3 -m json.tool "$WORK/skimmer.json" >/dev/null
publish dorkitude/scoop-bucket skimmer.json "$WORK/skimmer.json" "skimmer ${VERSION}"

echo "==> Done: brew install dorkitude/tap/skimmer · scoop install dorkitude/skimmer"
