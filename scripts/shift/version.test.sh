#!/usr/bin/env bash
set -euo pipefail
version_script=$(cd "$(dirname "$0")" && pwd)/version.sh
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
cd "$fixture"
git init -q
git config user.email test@example.com
git config user.name test
git commit -q --allow-empty -m initial

# shellcheck disable=SC1090
if (source "$version_script") 2>/dev/null; then
  echo '태그 없는 빌드가 허용되었습니다.' >&2
  exit 1
fi
git tag v0.4.43
git commit -q --allow-empty -m fork
git tag "shift-$(git rev-parse HEAD)"
git tag v0.4.44-rc.1
# shellcheck disable=SC1090
source "$version_script"
# shellcheck disable=SC2154
[[ "$upstream_version" == 0.4.43 ]]
# shellcheck disable=SC2154
[[ "$version" == "0.4.43+shift.$(git rev-parse HEAD | cut -c1-12)" ]]
echo '기반 태그 선택, 포크·prerelease 태그 제외, 태그 누락 거부 확인'
