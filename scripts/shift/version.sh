#!/usr/bin/env bash
# 호출한 저장소의 도달 가능한 정식 upstream 태그로 빌드 버전을 계산한다.
# 빌드·이미지·릴리스가 동일한 값을 쓰도록 source해서 사용한다.
revision=$(git rev-parse HEAD) || return 1
upstream_tag=$(git describe --tags --match 'v[0-9]*' --exclude '*-*' --exclude '*+*' --abbrev=0 HEAD) || {
  echo 'upstream 릴리스 태그가 필요합니다. 전체 이력과 태그를 가져오세요.' >&2
  return 1
}
[[ "$upstream_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "잘못된 upstream 릴리스 태그: $upstream_tag" >&2
  return 1
}
upstream_version=${upstream_tag#v}
# source한 호출자가 사용하는 출력 변수다.
# shellcheck disable=SC2034
version="${upstream_version}+shift.${revision:0:12}"
