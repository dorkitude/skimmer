#!/usr/bin/env bash
# Publish the .deb files in $1 (default dist/) to the dorkitude/skimmer-apt
# repository, which GitHub Pages serves at https://dorkitude.github.io/skimmer-apt.
#
# Runs in the release workflow. Needs dpkg-dev, apt-utils, and gpg, plus:
#   APT_GPG_PRIVATE_KEY  armored private key that signs the repository
#   APT_DEPLOY_KEY       SSH private key of a deploy key with write access
#                        to dorkitude/skimmer-apt (and nothing else)
set -euo pipefail

: "${APT_GPG_PRIVATE_KEY:?APT_GPG_PRIVATE_KEY is not set}"
: "${APT_DEPLOY_KEY:?APT_DEPLOY_KEY is not set}"

dist=$(cd "${1:-dist}" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if ! command -v dpkg-scanpackages >/dev/null || ! command -v apt-ftparchive >/dev/null; then
  sudo apt-get update -qq && sudo apt-get install -y -qq dpkg-dev apt-utils
fi

export GNUPGHOME="$work/gnupg"
mkdir -m 700 "$GNUPGHOME"
gpg --batch --quiet --import <<<"$APT_GPG_PRIVATE_KEY"
keyid=$(gpg --list-secret-keys --with-colons | awk -F: '$1=="sec"{print $5; exit}')

install -m 600 /dev/null "$work/deploy_key"
printf '%s\n' "$APT_DEPLOY_KEY" > "$work/deploy_key"
export GIT_SSH_COMMAND="ssh -i $work/deploy_key -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"

git clone -q git@github.com:dorkitude/skimmer-apt.git "$work/apt"
cd "$work/apt"
mkdir -p pool/main/s/skimmer
cp "$dist"/*.deb pool/main/s/skimmer/

for arch in amd64 arm64; do
  mkdir -p "dists/stable/main/binary-$arch"
  dpkg-scanpackages --multiversion --arch "$arch" pool/ > "dists/stable/main/binary-$arch/Packages"
  gzip -9 -k -f "dists/stable/main/binary-$arch/Packages"
done

apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=skimmer \
  -o APT::FTPArchive::Release::Label=skimmer \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=stable \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
  -o APT::FTPArchive::Release::Components=main \
  -o APT::FTPArchive::Release::Description="skimmer apt repository" \
  release dists/stable > dists/stable/Release

gpg --batch --yes --default-key "$keyid" -abs -o dists/stable/Release.gpg dists/stable/Release
gpg --batch --yes --default-key "$keyid" --clearsign -o dists/stable/InRelease dists/stable/Release
gpg --armor --export "$keyid" > key.gpg
touch .nojekyll

git config user.name "skimmer release"
git config user.email kyle@kylewild.com
git add -A
version=$(basename "$(ls "$dist"/skimmer_*_linux_amd64.deb | head -1)" _linux_amd64.deb)
git commit -qm "Publish ${version}" || echo "nothing to publish"
git push -q origin HEAD:main
