#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/shift/version.sh
release_tag="shift-${revision}"
for component in backend web; do
  image="ghcr.io/xxshiftxx/multica-${component}"
  refs=()
  for digest_file in digests/digest-"$component"-*/*; do
    digest=$(basename "$digest_file")
    [[ "$digest" =~ ^[a-f0-9]{64}$ ]]
    refs+=("${image}@sha256:${digest}")
  done
  [[ ${#refs[@]} == 2 ]]
  docker buildx imagetools create --tag "${image}:${release_tag}" "${refs[@]}"
  docker buildx imagetools inspect "${image}:${release_tag}" --format '{{json .Manifest}}' > "dist/shift/${component}-image.json"
done
jq -n --arg revision "$revision" --arg tag "$release_tag" \
  --arg version "$version" --arg upstream_version "$upstream_version" \
  --arg run "https://github.com/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}" \
  --arg backend "$(jq -r .digest dist/shift/backend-image.json)" \
  --arg web "$(jq -r .digest dist/shift/web-image.json)" \
  '{schema: 1, repository: "XxshiftxX/multica", revision: $revision, tag: $tag, version: $version, upstream_version: $upstream_version, workflow: $run,
    images: {backend: ("ghcr.io/xxshiftxx/multica-backend@" + $backend), web: ("ghcr.io/xxshiftxx/multica-web@" + $web)}}' \
  > dist/shift/release.json
cat > dist/shift/notes.md <<NOTES
버전: ${version}
기반 upstream: ${upstream_tag}
커밋: ${revision}

backend·web의 linux/amd64 및 linux/arm64 이미지와 Linux/macOS CLI를 같은 커밋에서 빌드했습니다.
배포 정보는 release.json, CLI 무결성 정보는 checksums.txt에 있습니다.

검증: ${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}
NOTES
# Keep a failed/retried publication out of the deployment feed until every asset is present.
if ! gh release view "$release_tag" --repo "$GITHUB_REPOSITORY" >/dev/null 2>&1; then
  gh release create "$release_tag" --repo "$GITHUB_REPOSITORY" --target "$revision" \
    --draft --title "Shift Multica ${version}" --notes-file dist/shift/notes.md
fi
gh release upload "$release_tag" --repo "$GITHUB_REPOSITORY" --clobber \
  dist/shift/*.tar.gz dist/shift/checksums.txt dist/shift/release.json
if [[ "$GITHUB_REF" == refs/heads/main ]]; then
  gh release edit "$release_tag" --repo "$GITHUB_REPOSITORY" --draft=false --prerelease=false --latest
else
  gh release edit "$release_tag" --repo "$GITHUB_REPOSITORY" --draft=false --prerelease --latest=false
fi
