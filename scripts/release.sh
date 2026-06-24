#!/usr/bin/env bash
# Cut a release: bump every version reference in one place, run the tests,
# commit the bump, and create an annotated tag.
#
#   scripts/release.sh v0.2.1      # or: make release VERSION=v0.2.1
#
# It deliberately does NOT push. Review the commit/tag, then publish:
#   git push origin <branch> && git push origin <tag>
# Pushing the tag triggers .github/workflows/release.yml, which builds the
# binaries and creates the GitHub Release that `nodexia update` pulls.
set -euo pipefail

VERSION="${1:-}"

die() {
  echo "release: $*" >&2
  exit 1
}

[ -n "$VERSION" ] || die "usage: scripts/release.sh vX.Y.Z (e.g. v0.2.1)"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  die "version must look like v1.2.3 (got '${VERSION}')"

# Always operate from the repository root.
cd "$(git rev-parse --show-toplevel)" || die "not a git repository"

# A dirty tree would leak unrelated changes into the release commit.
[ -z "$(git status --porcelain)" ] ||
  die "working tree is dirty — commit or stash changes first"

# Never clobber an existing release tag.
if git rev-parse -q --verify "refs/tags/${VERSION}" >/dev/null; then
  die "tag ${VERSION} already exists"
fi

echo "release: bumping version references to ${VERSION}"
sed -i -E "s/^VERSION \?= .*/VERSION ?= ${VERSION}/" Makefile
sed -i -E "s/^readonly DEFAULT_IMAGE_VERSION=.*/readonly DEFAULT_IMAGE_VERSION=\"${VERSION}\"/" scripts/install.sh
sed -i -E "s/(NODEXIA_IMAGE_VERSION=).*/\1${VERSION}/" .env.production.example
sed -i -E "s/(-X main\.version=)[^ \"]*/\1${VERSION}/" .github/workflows/test.yml

# Guard against a sed that silently matched nothing.
for f in Makefile scripts/install.sh .env.production.example .github/workflows/test.yml; do
  grep -q "${VERSION}" "$f" || die "failed to update version in ${f}"
done

git --no-pager diff --stat

echo "release: running tests"
go test ./...

echo "release: committing and tagging"
git add Makefile scripts/install.sh .env.production.example .github/workflows/test.yml
git commit -m "chore(release): ${VERSION}"
git tag -a "${VERSION}" -m "${VERSION}"

branch="$(git rev-parse --abbrev-ref HEAD)"
cat <<EOF

✓ Release ${VERSION} staged locally (commit + tag created, not pushed).

Publish it:
  git push origin ${branch}
  git push origin ${VERSION}

Pushing the tag runs the release workflow (builds binaries, creates the GitHub
Release). Then upgrade a server with:  nodexia update
EOF
