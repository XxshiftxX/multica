---
name: multica-deploy
description: Shift Multica 포크를 빌드·릴리스하고 shift-infra의 GitOps 경로로 k3s와 로컬 systemd 데몬에 배포한다. 포크 업데이트 배포, 특정 릴리스 배포, 배포 검증 또는 롤백 요청에 사용한다.
---

# Multica 배포

사용자가 지정한 포크 릴리스를 backend·web·데몬에 반영하고 실제 실행 상태를 확인한다.
이미 승인된 배포 범위는 PR 병합, Flux reconcile, 데몬 교체와 검증까지 이어서 수행한다.
같은 승인을 단계마다 다시 묻지 않는다. 준비만 요청받았다면 검증된 변경과 배포 PR을 제공한다.
이 스킬 자체가 새로운 배포나 데이터 복원을 승인하지는 않는다.

## 배포 경로와 기준

- 소스: `XxshiftxX/multica`, 보통 `~/workspaces/multica`.
- Production 선언: `XxshiftxX/shift-infra`의 `k3s/apps/web-apps/overlays/production/`.
- 릴리스: `shift-<40자리 source commit SHA>`. `release.json`에 backend·web digest가 있다.
- 데몬: 개발 서버의 사용자 `multica-daemon.service`, 바이너리는 `~/.local/bin/multica`.
- 공개 서비스: `https://multica.shiftryu.com`.

현재 저장소의 `AGENTS.md`와 infra 저장소의 지침을 따른다. 기존 승인 범위를 유지하면서
각 저장소의 PR 규칙을 적용한다. 변경 작업은 사용자의 수정이 없는 checkout/worktree에서 한다.
infra의 기존 checkout이 오래되었거나 dirty하면 `origin/main`에서 별도 worktree를 만든다.

읽을 자료:

- 빌드·릴리스 방식: [포크 릴리스 문서](../../../docs/shift/deployment.md).
- 배포·복구 명령의 기준: infra의 `docs/multica-deployment.md`와 그 문서가 가리키는 스크립트.
  로컬 checkout이 없으면 [현재 infra 문서](https://github.com/XxshiftxX/shift-infra/blob/main/docs/multica-deployment.md)를 읽는다.

## 릴리스 선택과 빌드

기존 릴리스 배포 요청이면 그 릴리스를 사용한다. 새 변경이나 upstream 업데이트가 필요하면
작업 브랜치에서 변경·검증하고 소스 PR을 만든다. upstream 반영은 `git fetch upstream` 후
`upstream/main`을 merge한다. 승인된 배포라면 PR 검사를 확인하고 병합까지 수행한다.

`Shift release`는 포크 `main` push에서 실행된다. 테스트·이미지·CLI 빌드와 `publish`까지
성공했는지 확인한다. 특정 릴리스를 기다릴 때는 workflow 이름뿐 아니라 source SHA도 맞춘다.

```bash
gh run list --repo XxshiftxX/multica --workflow shift-release.yml --branch main
gh release view "$release_tag" --repo XxshiftxX/multica
```

backend·web의 linux/amd64 및 linux/arm64 이미지와 Linux/macOS CLI를 같은 커밋에서 빌드한다.
일반 릴리스는 main에서 발행하며, 초기 구성 브랜치나 다른 ref의 수동 실행은 prerelease다.
사용자가 선택한 prerelease도 명시적으로 배포할 수 있다.

## Production 백업과 배포 PR

개발 서버에는 Production kubeconfig가 없고 직접 SSH도 허용되지 않을 수 있다.
기존 GitHub Actions의 Tailscale·SSH 연결을 사용한다. 자격 증명을 로컬로 복사하지 않는다.

배포 전 백업 workflow를 실행하고 성공을 확인한다.

```bash
gh workflow run verify-multica-production.yml --repo XxshiftxX/shift-infra \
  --ref main -f mode=preflight
```

이 작업은 현재 imageID와 Multica DB archive를 Production의
`/srv/shift-infra/backups/multica/`에 보관하고 DB에 연결하지 않고 archive 전체를 검사한다.
성공한 실행 URL과 백업 경로를 배포 기록에 남긴다. backend는 시작 시 migration을 실행하므로
선택한 버전의 migration 변경도 확인한다.

선택한 릴리스로 배포 PR을 생성한다. 기본 예약 실행은 매시간 최신 정식 릴리스를 찾는다.

```bash
gh workflow run update-multica.yml --repo XxshiftxX/shift-infra \
  --ref main -f release="$release_tag"
```

완료된 실행과 `automation/multica-release` 브랜치의 PR을 확인한다. 이미 동일 digest가
배포 선언에 있으면 새 PR이 생기지 않을 수 있으므로 현재 `multica-release.json`을 비교한다.
생성된 PR의 tag·두 이미지 digest가 요청한 릴리스와 일치하는지 확인한다.

자동 PR은 GITHUB_TOKEN으로 생성되어 별도 PR CI를 트리거하지 않는다. 생성 workflow가
수행한 `mise run validate`의 성공과 검증 대상 변경을 확인한다. 이후 PR이 수정되었으면
새 head를 검증한다. 수동으로 갱신할 때는 infra의 `scripts/update-multica-release.py`와
`mise run validate`를 사용한다.

배포 승인이 있으면 검증한 head를 지정해 Draft 해제·병합한다. 승인 범위에 포함된 병합을
추가 질문으로 미루지 않는다. 병합 후 배포를 포함하는 실제 main SHA를 기록한다.

## Flux와 서비스 확인

병합된 main SHA로 원격 검증을 실행하고 완료까지 확인한다.

```bash
gh workflow run verify-multica-production.yml --repo XxshiftxX/shift-infra \
  --ref main -f mode=verify -f expected_main_commit="$infra_sha"
```

이 workflow는 Flux source와 `web-apps` reconcile을 요청하고, 정확한 revision 수렴,
backend·frontend Deployment의 digest·rollout, `/readyz`와 웹 HTTP 응답을 확인한다.
실행 직전에 main이 전진했다면 원하는 이미지가 여전히 고정돼 있는지 확인하고 해당 SHA를 사용한다.
workload manifest는 PR → main → Flux 경로로 반영한다. 직접 `kubectl apply/edit/patch`하지 않는다.

## 데몬 업데이트

backend·web과 같은 릴리스를 사용한다. infra의 최신 스크립트가 있는 checkout에서 실행한다.
기본 실행과 `--check`는 다운로드·체크섬·버전 검증만 수행한다.

```bash
mise exec -- ./scripts/install-multica-daemon.sh "$release_tag" --check
curl -fsS http://127.0.0.1:19514/health \
  | jq '{status,cli_version,active_task_count,running_task_count}'
mise exec -- ./scripts/install-multica-daemon.sh "$release_tag" --restart
```

재시작 직전에 실행 중인 작업이 없는지 확인한다. 작업이 남아 있으면 완료를 기다리며
다른 배포 검증을 진행한다. 작업 중단이 필요하고 승인되지 않았다면 영향과 필요한 판단을 알린다.
health 포트는 기본 profile 기준이다. 다른 profile이나 머신을 요청받았으면 실제 서비스 설정을 따른다.

설치기는 바이너리를 백업하고 systemd 서비스를 중지·교체·시작한다. 추가 EnvironmentFile로
`MULTICA_DAEMON_AUTO_UPDATE=false`를 설정해 upstream 자동 업데이트를 끈다.
`multica update`는 upstream을 참조하므로 포크 업그레이드에 사용하지 않는다.

교체 후 systemd active, health의 `status=running`, 요청한 `cli_version`, 대상 서버와
runtime 등록을 확인한다. Secret이나 전체 process environment를 출력하지 않는다.

## 완료 보고와 실패 처리

배포한 릴리스, 병합 PR, Production 검증 실행, 데몬 버전과 백업 위치를 짧게 보고한다.
실제로 검증한 범위를 명시하고, 배포 설정 변경과 하네스 동작 변경을 구분한다.

검증 실패는 로그로 원인을 좁혀 같은 배포 범위 안에서 수정·재시도한다. 같은 원인으로
반복 실패하고 새로운 근거가 없으면 반복 실행을 멈추고 현재 상태와 필요한 조치를 알린다.
자동 복구를 위해 DB를 초기화하거나 데이터를 덮어쓰지 않는다.

롤백 요청이면 infra의 이전 image digest를 PR로 복원하고 같은 Flux 검증을 수행한다.
처음 upstream `latest`에서 전환한 경우에는 preflight에 기록한 실제 imageID를 사용한다.
데몬은 이전 릴리스나 설치기가 남긴 바이너리 백업으로 복구한다. 이미지 롤백은 DB migration을
되돌리지 않으므로 데이터 복구는 호환성과 별도 승인 범위를 확인한다.
