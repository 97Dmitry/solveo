#!/usr/bin/env bash
# Only invoked by the gated master push job. Never rebuild release assets here.
set -euo pipefail

: "${GH_REPO:?}" "${GITHUB_SHA:?}" "${GITHUB_RUN_NUMBER:?}"
[[ "$GITHUB_RUN_NUMBER" =~ ^[0-9]+$ ]]
tag="build-$GITHUB_RUN_NUMBER"

cd build/bin
test -s Solveo.exe
test -s SHA256SUMS.txt
sha256sum --check --strict SHA256SUMS.txt

# Concurrency ordering is not guaranteed. Recheck master under the release lock.
head_sha=$(gh api "repos/$GH_REPO/branches/master" --jq '.commit.sha')
if [[ "$head_sha" != "$GITHUB_SHA" ]]; then
  echo "Skip $tag: master has advanced beyond $GITHUB_SHA."
  exit 0
fi

# Query failure must stop publication, rather than being mistaken for an absent release.
existing=$(gh api "repos/$GH_REPO/releases?per_page=100" --paginate \
  --jq ".[] | select(.tag_name == \"$tag\") | [.draft, .target_commitish] | @tsv")
if [[ -n "$existing" ]]; then
  IFS=$'\t' read -r draft target <<< "$existing"
  if [[ "$draft" == false ]]; then
    release_sha=$(gh api "repos/$GH_REPO/commits/$tag" --jq '.sha')
    [[ "$release_sha" == "$GITHUB_SHA" ]] || {
      echo "Refuse to reuse $tag: its commit differs from $GITHUB_SHA." >&2
      exit 1
    }
    echo "$tag is already published; keep its original assets."
    exit 0
  fi
  [[ "$draft" == true && "$target" == "$GITHUB_SHA" ]] || {
    echo "Refuse to reuse $tag: unexpected draft target." >&2
    exit 1
  }
else
  gh release create "$tag" --target "$GITHUB_SHA" --title "Solveo $tag" \
    --generate-notes --draft
fi

# Finish uploading before making the release visible. A failed upload leaves a
# recoverable draft; reruns replace draft assets, never published release assets.
gh release upload "$tag" Solveo.exe SHA256SUMS.txt --clobber
gh release edit "$tag" --draft=false --latest
