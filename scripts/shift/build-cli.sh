#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/shift/version.sh
build_date=$(git show -s --format=%cI HEAD)
mkdir -p dist/shift
for target_os in linux darwin; do
  for target_arch in amd64 arm64; do
    archive="multica-${target_os}-${target_arch}.tar.gz"
    staging=$(mktemp -d)
    (
      cd server
      CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath \
        -ldflags "-s -w -X main.version=${version} -X main.commit=${revision} -X main.date=${build_date}" \
        -o "$staging/multica" ./cmd/multica
    )
    cp LICENSE NOTICE "$staging/"
    tar -C "$staging" -czf "dist/shift/$archive" multica LICENSE NOTICE
    rm -rf "$staging"
  done
done
(cd dist/shift && sha256sum ./*.tar.gz > checksums.txt)
