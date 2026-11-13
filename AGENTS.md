# Repository Instructions

Multica is a task management platform where people and agents collaborate on issues. These instructions apply to all coding agents working in this repository.

## Scope and Reading Order

- Before changing `apps/mobile/`, also read [apps/mobile/AGENTS.md](apps/mobile/AGENTS.md), even if your tool does not load nested instructions automatically. Platform-specific sections below apply only to the named platform.
- For naming, translations, or Chinese UI/docs copy, read [conventions.mdx](apps/docs/content/docs/developers/conventions.mdx) and [conventions.zh.mdx](apps/docs/content/docs/developers/conventions.zh.mdx).
- Maintain shared rules here and mobile-specific rules in the mobile file. `CLAUDE.md` files only import them. Update instructions in the same change that alters the referenced workflow or boundary; do not add incident timelines, dependency version lists, or duplicate rules.

## Sharing Rules and Package Boundaries

| Location | Responsibility and constraints |
| --- | --- |
| `server/` | Go backend; Chi, sqlc, WebSocket |
| `packages/core/` | Headless logic, API client, Query hooks, shared Zustand stores. No UI libraries, `react-dom`, `localStorage`, or `process.env`; use `StorageAdapter` for persistence. |
| `packages/ui/` | UI primitives and shared styles. No business logic or `@multica/core` imports. |
| `packages/views/` | Shared web/desktop pages and business components. No store definitions, `next/*`, or `react-router-dom`; use `NavigationAdapter`, `useNavigation()`, and `<AppLink>`. |
| `apps/web/` | Next.js routes/layouts and web-only UI. Framework APIs stay here; shared navigation adapters live in `apps/web/platform/`. |
| `apps/desktop/` | Electron and desktop-only UI/state. Application navigation goes through `apps/desktop/src/renderer/src/platform/`. |
| `apps/mobile/` | Independent Expo/React Native client: owns UI, state, hooks, providers, i18n, build, and release. Shares core types and pure utilities, including platform-independent schemas. |
| `apps/docs/` | Fumadocs documentation site |

- Dependency direction is `views -> core + ui`; core and ui remain independent. Shared packages export raw TypeScript compiled by consuming apps.
- Extract logic used by both web and desktop into the appropriate shared package. Keep framework/Electron APIs in the app layer; inject platform-specific UI through props/slots.
- Wire shared features into both web routes and the desktop router or overlay. Reuse existing guards/providers such as `DashboardGuard` in `packages/views/layout/`.
- Each workspace declares its directly imported external dependencies. Use `catalog:` for shared dependencies; mobile pins Expo/React Native dependencies in its own manifest.

## Development and Verification

Use `Makefile`, workspace `package.json` files, and `pnpm-workspace.yaml` for current commands and versions. See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and worktree operations.

- Use the checkout's managed environment: `make up`, `make status`, `make down`. `make down` preserves data; `make destroy` removes the environment and its data.
- Worktrees share PostgreSQL but have isolated databases/ports. Use the environment scripts and `.env.worktree`; do not copy the main checkout's `.env` or manually create a database through an assumed PostgreSQL instance.
- Regenerate sqlc with `make sqlc` after SQL changes.
- Run the narrowest useful checks while iterating, then broaden when risk warrants it. Report what actually ran and any skipped checks.

Run these from the repository root:

| Scope | Checks |
| --- | --- |
| Frontend excluding mobile | `pnpm typecheck`, `pnpm lint`, `pnpm test` |
| Go backend | `make test` |
| End-to-end | `pnpm exec playwright test` |
| Combined web/backend verification | `make check` |
| Mobile | Commands in [apps/mobile/AGENTS.md](apps/mobile/AGENTS.md#verification) |

Docs-only changes can use link/reference checks and `git diff --check`; state that code tests were not run.

## State Rules

- TanStack Query owns API/server data. Zustand owns client state such as filters, drafts, modals, and tab layout; persist only durable preferences/drafts/layout, not server data or ephemeral UI state.
- Web/desktop shared stores live in `packages/core/`. Desktop platform stores remain in desktop; mobile stores remain in mobile. Do not define stores in `packages/views/`.
- On web/desktop, workspace identity is route-driven; platform mirrors exist only for request headers, storage namespaces, and reconnects. React Context is for platform plumbing, not a second server-state store.
- Among stores, only auth/workspace stores may call `api.*` directly; other server interactions belong in queries/mutations.
- Workspace-scoped query keys include `wsId`; account-level keys remain account-scoped. Hooks needing workspace context accept `wsId` unless guaranteed to run under its provider.
- Zustand selectors return stable references; use shallow comparison for allocated objects/arrays.
- WebSocket events patch or invalidate Query caches, not server payloads in Zustand. Clearing client-owned pointers is allowed with one responder and a self-initiated guard when this client can cause the event.
- Optimistic field patches require a predictable result, rare failure, trivial rollback, and staying on the current screen. Snapshot before patching, roll back on failure, and invalidate uncertain projections on settle.
- Create/delete/leave and confirmation flows await the server before navigation or cleanup; do not optimistically delete entities. Exceptions: workspace leave (known debt under Desktop Rules) and mobile inbox mark-read (mobile instructions).
- Message sends use visible pending state and retry on failure.

## API Compatibility

Installed desktop clients may talk to newer backends. Preserve response compatibility at the API boundary.

- UI-consumed JSON passes through a zod schema and `parseWithFallback`, not an `as T` cast. Web/desktop use `packages/core/api/schema.ts`; mobile uses its own request helpers.
- Provide defaults for optional fields and fallbacks for unknown server enums. Prefer explicit boolean checks; avoid tying critical affordances to a single backend flag when other contract signals are available.
- When adding/changing an endpoint, update its schema and malformed-response tests.

## Database and Migration Rules

- Do not add foreign keys, cascading deletes, or cascading updates. Validate relationships and clean up dependents in application code, using a transaction when the operation must be atomic.
- Every migration-created index, including indexes on new tables, uses `CREATE [UNIQUE] INDEX CONCURRENTLY`. Each concurrent index build gets its own single-statement migration file; the runner executes files outside an explicit transaction.
- Conditionally skipped migrations are still recorded in `schema_migrations`. Later DDL touching conditional objects must be idempotent (`IF EXISTS` / `IF NOT EXISTS`); document recovery if the missing object would break runtime behavior.

## Backend UUID Rules

In `server/internal/handler/`, distinguish UUID sources before using them in writes:

- UUID-or-human-readable resource params: resolve with loaders such as `loadIssueForUser`, `loadSkillForUser`, `loadAgentForUser`, or `requireDaemonRuntimeAccess`, then write using the resolved `entity.ID`.
- Pure UUID request input: `parseUUIDOrBadRequest(w, s, fieldName)`; return immediately when `ok=false`.
- Trusted sqlc/test-fixture round-trips: `parseUUID(s)`, which panics on invalid input.
- Outside handlers: `util.ParseUUID(s)` and check the error.

Workspace-scoped queries filter by `workspace_id`; membership gates access and `X-Workspace-ID` selects the workspace. Assignees are polymorphic: interpret `assignee_id` together with `assignee_type`.

## Desktop Rules

- Workspace session routes are tab destinations. Pre-workspace one-shot flows (create workspace, accept invite) use `WindowOverlay` in `apps/desktop/src/renderer/src/stores/window-overlay-store.ts`, not new routes. Stale workspace tabs heal by dropping stale tab groups.
- Workspace route layouts own `setCurrentWorkspace(slug, uuid)` from `@multica/core/platform`; leaving workspace context calls `setCurrentWorkspace(null, null)`.
- Cross-workspace navigation uses the adapter's `switchWorkspace(slug, targetPath)` flow; do not bypass it with direct router navigation.
- Workspace leave clears/navigates first to avoid the `member:removed` race; this is known debt in `packages/views/settings/components/workspace-tab.tsx`, not a pattern for new flows.
- Full-window views outside the dashboard shell mount `<DragStrip />` from `@multica/views/platform` as the first flex child. Interactive controls in the top 48px need `WebkitAppRegion: "no-drag"`.

## UI Copy

- Descriptions are optional and omitted by default. Do not restate titles, labels, values, statuses, or button actions; add help only for a non-obvious choice, constraint, consequence, or next step. A description prop is not a requirement to write a paragraph.
- Before adding or reviewing copy, follow "UI descriptions" in [conventions.mdx](apps/docs/content/docs/developers/conventions.mdx), and review all supported translations, including mobile's independent copy.

## Web/Desktop UI Rules

- For Button and Dialog usage, read `packages/ui/docs/button.md` and `packages/ui/docs/dialog.md`.
- Prefer existing shadcn/Base UI primitives. Add components with `pnpm ui:add <component>`.
- For `pnpm ui:add @reui/<name>`, decline overwrite prompts. Keep `REUI_LICENSE_KEY` in the environment, never in repo files. Adapt vendored primitives into `packages/ui/components/ui/` and compositions into `packages/views/`.
- Import icons from `@multica/ui/icons`; no icon library is installed. If the icon you need is missing, draw it by following `.agents/skills/multica-icons/SKILL.md`. Do not substitute an inline SVG or an emoji. Rewrite icon imports in `ui:add` output before committing.
- Use shared semantic tokens in `packages/ui/styles/`. Typography uses the role-named `--text-*` scale in `packages/ui/styles/tokens.css`, not Tailwind's default size ramp.
- Selected states remain identifiable on hover. Handle overflow, long text, and scrolling deliberately; avoid unnecessary local state and dividers.

## Testing

- Tests live beside their implementation: shared logic in core, shared components in views, platform wiring in apps, E2E in `e2e/`, Go tests in server. Do not test shared behavior in app suites.
- Give each behavior one canonical test layer: helper tests own parsing/state matrices; component tests cover wiring, accessibility, happy paths, and named regressions. Prefer a failing regression test before behavioral fixes.
- DOM-free `.test.ts` files start with `// @vitest-environment node`; do not use it if it would silently switch the code under test to an SSR path.
- Every workspace with jsdom suites loads `@multica/vitest-setup/web-storage` in its Vitest setup; do not add per-package `localStorage` shims.
- Views tests must not mock `next/*` or `react-router-dom`. Mock stores with their Zustand callable shape plus `getState`; mock API calls at `@multica/core/api`.
- E2E setup/teardown uses `TestApiClient`.
- DB-backed Go tests use `server/internal/testutil` fixtures (`dbfx.Issue`, `dbfx.Task`, `dbfx.Insert`) and `testutil.Call(h, req).Want(status).JSON(&out)`. Keep product assertions and case-specific diagnostics in the test, not fixture helpers.
- Default tests must not resolve or execute user-installed agent CLIs; pass test-created fake or missing executable paths. New default agent commands go in `scripts/agent-cli-command-names.txt`.
- 실제 에이전트 연동 테스트는 명시적으로 허가받은 경우에만 실행한다. `agentintegration` 빌드 태그로 분리하고, 실행 파일 탐색이나 계정 접근 전에 `MULTICA_RUN_REAL_AGENT_TESTS=1`을 확인한다. 실행할 테스트를 지정한다: `(cd server && MULTICA_RUN_REAL_AGENT_TESTS=1 go test -tags=agentintegration ./pkg/agent -run '<test-name>' -count=1 -v)`.
- 테스트 이름과 설명은 검증 대상을 구체적으로 쓴다. 간단한 검증은 “기본 동작 테스트”, “빌드 확인”, “연동 테스트”처럼 이해하기 쉬운 표현을 사용한다.

## Change and Delivery Rules

- Keep changes scoped; reuse existing patterns.
- PR 제목·본문, 이슈 설명, 리뷰, 커밋 메시지의 설명 부분, 코드 주석, 개발 문서는 기본적으로 한국어로 작성한다. 기존 내용을 수정할 때는 변경하는 부분부터 적용하며, 언어 통일만을 위한 일괄 번역은 하지 않는다. 코드 식별자·명령어·API 이름과 Conventional Commit 접두사(`feat:`, `fix:` 등)는 원형을 유지한다. 제품 UI와 번역 파일은 해당 로케일의 언어 규칙을 따른다. 사용자가 별도 언어를 지정하면 그 요청을 우선한다.
- Before adding complexity for a speculative or vanishingly unlikely race or security edge case, explain the concrete failure mode, likelihood, and complexity cost to the user and get their buy-in. Do not silently expand scope for theoretical robustness.
- Do not add internal compatibility shims, dual writes, fallback paths, or legacy adapters unless requested. This does not relax API response compatibility above.
- Use names, types, and tests to express behavior. Keep comments for non-obvious rationale and constraints; remove superseded plans and incident narratives when updating code. Preserve tool directives and mixed-version contracts.
- New global pre-workspace routes use a single word or `/{noun}/{verb}`, not hyphenated root names. Update `server/internal/handler/reserved_slugs.json`, run `pnpm generate:reserved-slugs`, and commit `packages/core/paths/reserved-slugs.ts` when changing reserved slugs.
- Use atomic conventional commits and the repository PR template. For releases, follow [.github/RELEASING.md](.github/RELEASING.md); default to a patch bump unless specified otherwise.

## Shift Deployment Skill

For Shift fork releases and deployment to k3s or the local daemon, use
[`.agents/skills/multica-deploy/SKILL.md`](.agents/skills/multica-deploy/SKILL.md).

승인된 일반 이미지 배포는 필수 CI·릴리스 발행 → 정상 GitOps 변경 → 대상 버전·rollout·health와 변경한 핵심 기능 확인으로 끝낸다. 직전 정상 digest와 기존 revert 절차를 기록하되, 매 배포마다 이전 이미지 manifest 조회·재pull·실제 롤백 시험이나 전체 운영 회귀를 새 관문으로 요구하지 않는다. 제품팀이 배포·확인을 소유하고 기존 운영 CI 접근을 먼저 사용한다. 실제 공유 기반 문제만 SRE에 넘긴다.

DB migration·데이터 형식의 비호환, 새 복구 방식, 비가역 데이터 변경, secret 원본·권한·신뢰 경계·보호 우회는 별도 판단한다. 기존 백업의 유효성과 migration 변경을 확인하며 새 dump/restore 시험을 모든 릴리스에 강제하지 않는다. 최종 SHA 필수 CI·브랜치 보호·기존 리뷰, 데몬의 실행 중 작업 보호와 API 호환성 규칙은 유지한다. 공통 기준은 [shift-infra 배포 문서](https://github.com/XxshiftxX/shift-infra/blob/main/docs/deployment.md)를 따른다.
