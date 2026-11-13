# Shift 포크 릴리스

## 업데이트

원본 변경은 `upstream/main`을 작업 브랜치에 merge하고 PR로 `main`에 반영한다.
`.github/workflows/shift-release.yml`은 `main` push마다 다음 결과물을 만든다.

- backend·web: linux/amd64, linux/arm64 GHCR 이미지
- CLI·데몬: Linux/macOS의 amd64, arm64 archive와 SHA-256 체크섬
- `release.json`: 표시 버전(`version`), upstream 기반 버전(`upstream_version`), 커밋, 이미지 digest, 빌드 실행 URL

배포 태그는 `shift-<전체 commit SHA>`를 유지한다. CLI·서버·웹 표시 버전은
`0.4.43+shift.affe37e3f744`처럼 upstream 기반 버전과 포크 커밋을 함께 담는다.
`scripts/shift/version.sh`가 해당 커밋에서 도달 가능한 가장 가까운 정식 `vX.Y.Z`
태그를 선택한다. 이는 upstream의 최신 버전이 아니라 실제 포함된 릴리스 기준이다.
CI는 포크의 전체 이력과 함께 upstream 저장소의 `v*` 태그를 직접 가져온다.
upstream 병합만으로 태그가 포크에 전파되지는 않으므로, 포크 태그만 사용하면 이전 버전으로
표시될 수 있다. 기반 태그를 찾지 못하면 빌드를 중단한다.
SHA만으로는 CLI 최소 버전 검사를 통과할 수 없으므로 이 형식을 모든 빌드에 동일하게 적용한다.

모든 Go 테스트와 이미지·CLI 빌드가
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

일반 배포는 발행된 source·digest와 직전 정상 digest를 기록하고, 필수 CI·기존 리뷰·
브랜치 보호를 지켜 반영한 뒤 대상 버전·rollout·health·변경한 핵심 기능만 확인한다.
매번 이전 이미지 조회·재pull·실제 롤백 시험이나 전체 운영 회귀를 새 조건으로 요구하지
않는다. 기존 백업 근거를 재사용하며 migration 변경이나 백업 근거 부족 때만 필요한
추가 백업/복구 검증을 선택한다. 이미지 revert는 DB migration을 되돌리지 않으므로
비호환·비가역 변경은 별도 명시 결정이 필요하다.

제품팀은 기존 운영 CI 접근으로 배포·확인을 직접 수행한다. 로컬 접근 실패만으로
개인 인증 권한 확대를 요구하지 않는다. 공유 기반 점검·이미지 보존·복구 훈련은
SRE의 평시 책임이며 [공통 배포 기준](https://github.com/XxshiftxX/shift-infra/blob/main/docs/deployment.md)을 따른다.

데몬 배포는 infra 저장소의 `scripts/install-multica-daemon.sh`를 사용한다.
`multica update`는 upstream 바이너리를 받으므로 포크 업데이트에 사용하지 않는다.

## 권한과 재실행

포크 workflow는 저장소의 GITHUB_TOKEN으로 contents/packages를 발행한다.
upstream Homebrew나 desktop release 설정은 사용하지 않는다.
동일 커밋 재실행은 같은 릴리스의 자산을 갱신한다. production은 태그 대신 기존
릴리스에서 선택한 digest를 유지하므로 재빌드만으로 배포 버전이 변하지 않는다.
