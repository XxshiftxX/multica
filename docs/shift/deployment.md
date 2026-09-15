# Shift 포크 릴리스

## 업데이트

원본 변경은 `upstream/main`을 작업 브랜치에 merge하고 PR로 `main`에 반영한다.
`.github/workflows/shift-release.yml`은 `main` push마다 다음 결과물을 만든다.

- backend·web: linux/amd64, linux/arm64 GHCR 이미지
- CLI·데몬: Linux/macOS의 amd64, arm64 archive와 SHA-256 체크섬
- `release.json`: 커밋, 이미지 digest, 빌드 실행 URL

`shift-<전체 commit SHA>` 태그로 버전을 구분한다. 모든 Go 테스트와 이미지·CLI 빌드가
성공한 뒤 릴리스를 공개한다. 이미지 빌드 중 실패하면 digest만 남을 수 있지만 배포
릴리스에는 포함되지 않는다. 최초 구성 검증용 `shift/deployment-pipeline` 브랜치는
prerelease만 발행한다. 다른 브랜치의 수동 실행도 prerelease다.

포크 이미지 경로는 `ghcr.io/xxshiftxx/multica-backend`와
`ghcr.io/xxshiftxx/multica-web`이다. 최초 발행 후 GitHub Packages에서 두 패키지를
public으로 설정한다. k3s는 별도 registry credential 없이 digest로 pull한다.

## 배포

`XxshiftxX/shift-infra`의 `Multica 배포 PR` workflow가 매시간 정식 Shift 릴리스를 읽고
이미지 변경 Draft PR을 만든다. 수동 실행 시 prerelease 태그도 지정할 수 있다.
추가 cross-repository credential은 사용하지 않는다.
PR을 병합하면 Flux가 배포한다. backend는 시작 시 DB migration을 실행하므로
migration 호환성과 기존 백업을 배포 PR에서 확인한다.

데몬 배포는 infra 저장소의 `scripts/install-multica-daemon.sh`를 사용한다.
`multica update`는 upstream 바이너리를 받으므로 포크 업데이트에 사용하지 않는다.

## 권한과 재실행

포크 workflow는 저장소의 GITHUB_TOKEN으로 contents/packages를 발행한다.
upstream Homebrew나 desktop release 설정은 사용하지 않는다.
동일 커밋 재실행은 같은 릴리스의 자산을 갱신한다. production은 태그 대신 기존
릴리스에서 선택한 digest를 유지하므로 재빌드만으로 배포 버전이 변하지 않는다.
