# yt-dl-go Roadmap v2

> Successor to [`roadmap.md`](roadmap.md). Roadmap v1 (Milestones 1–6) is complete apart from two deliberately deferred items: native tray and automatic self-update. v2 comes from a full review of the backend engine, the API/persistence/security layers, the React frontend, and build/CI/release/docs as of `63f3658`.
>
> **Last updated:** 2026-09-24

## Merged fixes and source branches (2026-09-23)

The Milestone 0 fixes below were consolidated from the stacked source branches and squash merged by [PR #10](https://github.com/ajbergh/yt-dl-go/pull/10) as `287928e`. The independent 4K fix was squash merged by [PR #11](https://github.com/ajbergh/yt-dl-go/pull/11) as `677c59e`. Both are on `main`; the source branches record where each fix was developed.

| Work | Branch / commit | Current state |
| --- | --- | --- |
| M0.7 fresh checkout build | `fix/fresh-checkout-build` / `baa8887` | Merged by #10; clean Go build and tests passed. |
| M0.4 security headers | `fix/security-headers-m07` / `2c61f5d` | Merged by #10; Go tests passed. |
| M0.8 persistence errors | `fix/persistence-errors` / `cf34220` | Merged by #10; Go tests and vet passed. |
| M0.9 lint gate | `fix/lint-gate` / `45f4f1d` | Merged by #10; lint and typecheck pass. |
| M0.1 safe retention | `fix/safe-retention` / `53a3abb` | Merged by #10; storage-mode and scratch cleanup tests pass. |
| M0.2 active job cap | `fix/active-job-cap` / `a8f6c58` | Merged by #10; 100 retained Library records do not block admission. |
| M0.3 remove preview scaffolding | `fix/remove-preview-scaffolding` / `46605dc` | Merged by #10; production bundle scan passes. |
| M0.5 dev-only origins | `fix/dev-only-origins` / `8291aa0` | Merged by #10; release and dev origin checks pass. |
| 4K adaptive capture | `fix/4k-browser-representation` / `de754d7` | Merged by #11; the reported URL completed at 2160p with audio in a 2,335,115,476-byte WebM. |

All six jobs passed in [PR #10 CI](https://github.com/ajbergh/yt-dl-go/actions/runs/35870589146) and in [PR #11 CI after updating onto the merged stabilization commit](https://github.com/ajbergh/yt-dl-go/actions/runs/35871657223). The owner selected MIT for M0.6; its license and notice work merged in [PR #14](https://github.com/ajbergh/yt-dl-go/pull/14).

## Why a v2

v1 built the features for a "private local media acquisition and library application." The review found that the foundations underneath those features have not caught up:

1. **The Library is not durable.** Finished jobs are pruned after `RETENTION` (default 24h). With the `managed-only` storage policy that pruning **permanently deletes the only copy of the media**. `MAX_JOBS` (default 32) counts finished jobs too, so it effectively caps the Library at 32 downloads.
2. **Leftover app-builder scaffolding ships in production.** Combined with the missing frame protection, this lets any website iframe the local UI, receive its console output, and attempt clickjacking on destructive actions.
3. **The project has no LICENSE**, and the third-party notices cover 10 of the 31 Go modules linked into the binary.
4. **Engine reliability gaps:** no stall detection, a whole-playlist timeout, resume only on one transfer path, non-atomic publishing, and nondeterministic filenames.
5. **Scaling limits:** the whole job is rewritten under a global lock on every change, and every progress tick sends a full job snapshot that re-renders the entire UI.

v2 fixes these first (Milestone 0). Later milestones cover durability, reliability, features, UX, and engineering quality.

## Status legend

Same as v1:

- [ ] Planned
- [~] In progress
- [x] Implemented
- [!] Blocked or needs a design decision
- [T] Implemented but runtime/test validation still required

Priority tags: **P0** = correctness/safety/legal, do first · **P1** = high value · **P2** = valuable, schedule when convenient · **P3** = exploratory.

Evidence references are `file:line` at commit `63f3658` and will drift as code changes.

## Engineering guardrails (carried forward, with additions)

v1 guardrails 1–10 still apply. v2 adds:

11. **User media is never deleted by a background process.** Only an explicit user action may delete media, and only when at least one other copy remains or the user has confirmed.
12. **Every persisted-state change is observable on failure.** A DB write error must not be silently discarded.
13. **Production bundles contain only product code.** No preview-host, builder, or dev-telemetry code ships.
14. **Security headers are part of the contract.** CSP and frame protection are covered by regression tests like any other API behavior.
15. **Every linked dependency is attributed.** Notices are generated and CI fails if they drift from the actual dependencies.

---

# Milestone 0 — Stabilization, safety, and legal (all P0)

Goal: remove data-loss paths, close the framing/console exposure, and make the project legally distributable. These are small, targeted changes, and none should wait for the larger refactors.

### M0.1 Retention must never delete the only copy of user media

**Status:** [x] · **P0** · **Area:** backend

`prune()` (`src/server/worker.go:2329-2354`) removes every terminal job older than `RETENTION` (default `24h`, `main.go:143`). It calls `removeManagedCopies` (`output.go:325`, `os.RemoveAll(j.dir)`) and then `store.deleteJob`, with no check on storage mode. Under `managed-only`, published output does not exist, so the media is gone for good.

#### Scope

- Immediate fix: skip pruning any job whose files have no available published copy.
- Change the default `RETENTION` to "never" for Library records. Keep time-based cleanup only for failed/cancelled job scratch data.
- Add regression tests for pruning under all three storage modes.

#### Acceptance criteria

- A `managed-only` completed job survives any number of prune cycles.
- A `managed-published` job's Library record survives pruning unless retention was explicitly enabled.
- The README and Settings text explain exactly what retention removes.

`RETENTION` now defaults to `never` for Library records. Empty failed/cancelled jobs retain the previous 24-hour scratch cleanup by default. An explicit duration can prune a Library record only after every finalized media file and managed caption sidecar has a verified published copy. Regression cases cover all three storage modes, missing published media, scratch jobs, and configuration parsing on `fix/safe-retention`.

### M0.2 Stop `MAX_JOBS` from capping the Library

**Status:** [x] · **P0** · **Area:** backend

`server.go:846` rejects new jobs when `len(s.jobs) >= s.cfg.maxJobs`, and `s.jobs` includes finished jobs. The error message even says "wait for retained jobs to expire".

#### Scope

- Count only queued, active, and paused jobs against `MAX_JOBS`, and rename or document it as a concurrency/backlog cap.
- Size the scheduler queue channel independently of the Library size (`main.go:189`).
- Add a test showing that 100 finished jobs do not block a new job.

`MAX_JOBS` now counts queued, downloading, processing, and paused jobs. Terminal Library records no longer consume capacity. The scheduler uses its existing change signal instead of a size-limited token channel; single-item playlist retries obey the same cap. Tests cover 100 finished records, full-cap rejection, recovery after cancellation, paused jobs, and retry admission on `fix/active-job-cap`.

### M0.3 Remove preview-host / app-builder scaffolding from production

**Status:** [x] · **P0** · **Area:** frontend / security

The production bundle contains the following:

- `src/lib/cowork-parent-transport.ts:35-43`: hard-coded Microsoft preview-host origins.
- `src/lib/console-capture.ts`:
  - `:141-154` globally replaces `console.log/warn/error`.
  - `:32-42` posts the output to `window.location.ancestorOrigins[0]`, which is whatever page frames the app.
  - `:66-94` POSTs errors to `./__dev/console`.
- The 511-line `app-view-serializer.ts`, kept alive by the runtime `isPreviewMount()` call.
- `aether:*` performance marks.

#### Scope

- Delete `console-capture.ts`, `cowork-parent-transport.ts`, `app-mounted-transport.ts`, `app-view-transport.ts`, `app-view-signal.tsx`, `app-view-serializer.ts`, and `types/app-message.ts`.
- Remove their call sites in `main.tsx`, `App.tsx:6-7,30,48`, and `error-boundary.tsx`.
- Fix `getRouterBasename` (`App.tsx:14-26`) so unknown paths reach the not-found route.
- Add a build check that fails if `dist/assets/*.js` contains `ancestorOrigins`, `m365.cloud.microsoft`, or `__dev/console`.

The seven preview transport/serializer modules and their production call sites are removed on `fix/remove-preview-scaffolding`. Unknown paths now use the root router basename and reach the not-found route. `npm run build:check` builds into a temporary directory and scans every JavaScript asset for the three forbidden strings; CI runs it on pull requests. Frontend typecheck and lint pass.

### M0.4 Security headers: CSP and frame protection

**Status:** [x] · **P0** · **Area:** backend / security

Only `Cache-Control`, `Referrer-Policy`, `X-Content-Type-Options`, and `Vary` are set (`server.go:309-312`). Any site can iframe `http://127.0.0.1:8080`. Requests from inside the frame are same-origin, so they pass the Host/Origin checks and destructive buttons can be clickjacked.

#### Scope

- On every response, send a same-origin CSP with `script-src 'self'`, `frame-ancestors 'none'`, `base-uri 'none'`, and `form-action 'self'`, plus `X-Frame-Options: DENY`. `style-src 'unsafe-inline'` preserves the app's current dynamic React styles; `img-src` includes the specific YouTube image hosts already accepted by thumbnail validation.
- Add header regression tests for static, API, and ticket responses.

### M0.5 Allow the dev-server origins only in development builds

**Status:** [x] · **P0** · **Area:** backend / security

The default `ALLOWED_ORIGINS` includes `http://localhost:5173` and `http://127.0.0.1:5173` (`main.go:97`) in release builds. Any local process serving port 5173 therefore becomes a trusted origin.

#### Scope

Move the Vite origins behind a `dev` build tag or an explicit `--dev` flag. Release builds trust only their own listener origin.

The default Vite origins now compile only with `-tags=dev`; release builds allow their own configured loopback listener origin by default. `ALLOWED_ORIGINS` remains an explicit override. The documented manual development command and `dev:all` use the dev build tag. Release and dev origin checks run in CI on `fix/dev-only-origins`.

### M0.6 Add a project LICENSE and complete third-party notices

**Status:** [T] Implemented and dispatch-validated; tagged source-package validation pending · **P0** · **Area:** legal

- The root `LICENSE` now applies MIT to the project.
- Go notices cover the application dependency packages and Go runtime; npm notices cover production dependencies, including the Geist font (OFL-1.1) and lucide (ISC).
- Complete license texts are packaged under `src/server/licenses/`; the LGPL decoder source and local replacement build instructions are included.
- Tagged releases now prepare a source archive so users can rebuild against a modified LGPL decoder.

#### Scope

- [x] Choose MIT and add the root project license.
- [x] Document the LGPL relink path and include a source archive with each release.
- [x] Generate notices with pinned `go-licenses` and `license-checker` tools in CI; fail when generated files drift.
- [x] Ship the dependency license texts and LGPL source in release packages.

**Progress (2026-09-23):** Merged by [PR #14](https://github.com/ajbergh/yt-dl-go/pull/14). All six CI jobs passed, including reproducible license checks and Linux, Windows, and macOS package builds. Notices cover 33 Go/runtime entries and 15 npm production packages. Dry run [35925902083](https://github.com/ajbergh/yt-dl-go/actions/runs/35925902083) successfully created and checksummed the LGPL source/relink archive; the tag-triggered release path remains to be exercised when a release is cut.

### M0.7 A fresh clone must build and test

**Status:** [x] · **P0** · **Area:** DX

`//go:embed dist` (`src/server/static.go:13`) needs `src/server/dist`, which is gitignored. A tracked `.gitkeep` plus `//go:embed all:dist` keeps the Go embed target available in a fresh checkout; the `all:` prefix is needed because Go otherwise ignores dotfiles. If `index.html` is absent, the server returns a clear 503 build hint.

#### Scope

Track `src/server/dist/.gitkeep` with a `.gitignore` exception, embed all files under `dist`, and serve a clear "UI not built" page with the frontend build command when assets are missing.

### M0.8 Surface swallowed persistence errors

**Status:** [x] · **P0** · **Area:** backend

- `persistJobLocked` ignores `saveJob` errors (`server.go:203`).
- `savePart`/`deletePart` ignore theirs (`store.go:739, 749`).
- `f.Seek`/`f.Truncate` in the range loop are unchecked (`worker.go:2095-2096`).
- `muxMP4` ignores its `Close` error (`worker.go:2229`).

#### Scope

Log these failures and fail the job or item where state is lost. Job, checkpoint, settings, queue, and finalization persistence failures contribute to a consecutive failure count; a successful persistence operation resets it. `/api/health` reports degraded persistence after three consecutive failures. Failed job-state saves stop the job and expose a failure to the API/UI.

### M0.9 Gate CI on lint

**Status:** [x] · **P0** · **Area:** CI

Before this branch, `npm run lint` reported 49 production errors, mostly unused imports in `home.tsx:15-30`. CI did not run lint, and the command also scanned `dev_mock_new_ui`.

#### Scope

- Add `dev_mock_new_ui` to the ESLint ignores.
- Fix the existing errors.
- Enable `noUnusedLocals`/`noUnusedParameters` (`tsconfig.app.json:17-18`).
- Add `npm run lint` to `ci.yml`.

The production lint gate now excludes the separate `dev_mock_new_ui` prototype, removes unused production imports and locals, and enables TypeScript's unused checks. Lint passes with 21 existing warnings; typecheck passes.

---

# Milestone 1 — Durable library and persistence

Goal: make the "searchable local media library" durable over months and thousands of items instead of about a day.

### M1.1 Separate Library records from download jobs

**Status:** [x] Durable Library storage, independent lifecycles, and 10,000-item acceptance complete · **P1** · **Area:** backend / data model

The durable Library read path is decoupled from live job state. This milestone separates Library item lifecycle from job history and checks the 10,000-item boundary.

#### Scope

- Introduce a durable `library_items` model (one row per finalized media file) with job provenance.
- Jobs become transient work units. Library items outlive them.
- Migrate existing completed jobs into Library items.
- Removing a job from the queue does not remove its Library item, and vice versa, with explicit, documented actions for each.

#### Acceptance criteria

- The Library can hold 10,000+ items with no in-memory job state kept for finished work.
- All v1 deletion scopes (P0.3) still behave as documented.

**Progress (2026-09-24):**

The durable storage foundation merged in [PR #51](https://github.com/ajbergh/yt-dl-go/pull/51) (`64c501b`): `library_items` stores one row per finalized file, including migration backfill and transactional mirroring. [PR #53](https://github.com/ajbergh/yt-dl-go/pull/53) (`7d3fce5`) added the direct Library read API and UI. Library reads no longer depend on the in-memory job map, but their lifecycle still depended on job history. [PR #62](https://github.com/ajbergh/yt-dl-go/pull/62) (`87b4965`) added compact, transactionally maintained `library_sources` metadata for facets, filtering, and pagination.

[PR #67](https://github.com/ajbergh/yt-dl-go/pull/67) merged as `0760f83`. Startup loads active jobs only; finished jobs leave memory after their final event and hydrate on demand for existing actions. Retention looks up candidate IDs and loads each record individually. The final check set passed Go and race validation, frontend integration and browser E2E, lint/vet, CodeQL, and Linux, Windows, and macOS builds. CI found and fixed compatibility assumptions in old history assertions, missing M4A media-type restoration during hydration, and an unused queue-loader wrapper.

`feat/library-items-outlive-jobs` merged as [PR #68](https://github.com/ajbergh/yt-dl-go/pull/68) (`d86a8a0`). Migration v26 retains the published-output root with durable Library source metadata; a history-only DELETE API and Queue action, Library-backed file actions and tickets, and retention that persists managed-copy state before deleting history make the lifecycles independent. The existing retention fixture now expects Library rows to survive. The original `DELETE /api/jobs/{id}` and managed/published/all scopes remain intact. All PR checks passed, including Go/race/source validation, frontend integration and browser E2E, lint/vet, CodeQL, and Linux, Windows, and macOS builds.

`feat/library-item-removal` merged as [PR #69](https://github.com/ajbergh/yt-dl-go/pull/69) (`7e1b47a`). Migration v27 and durable per-file exclusions, a source-qualified item DELETE route, and a Library file action remove one app-managed file while preserving its job history, grouped siblings, and published output. The exclusion prevents later job saves from restoring the Library row. All PR checks passed, including Go/race/source validation, frontend integration and browser E2E, lint/vet, CodeQL, and Linux, Windows, and macOS builds.

`feat/library-10k-validation` merged in [PR #70](https://github.com/ajbergh/yt-dl-go/pull/70) as `53850fa`. Its `TestLibraryPaginationHandlesMoreThanTenThousandFilesWithoutRetainingFinishedJobs` seeds 10,100 durable files across 101 source groups, restarts with a persisted terminal sentinel, and exercises the public paginated API for stable two-page grouping, global facets/stats, and a filtered full-group result. The focused test passed locally in 4.3s; all PR checks passed, including Go/race/source validation, frontend integration and browser E2E, lint/vet, CodeQL, and Linux, Windows, and macOS builds.

### M1.2 Normalize queue items and write incrementally

**Status:** [x] · **P1** · **Area:** persistence / performance

`queue_items` is a JSON blob of up to 10,000 items (`store.go:485, 639, 798`). Every `saveJob`:

- rewrites the job row;
- deletes and reinserts all `job_files` and `job_failures` rows, with an O(n²) file loop (`store.go:669-676`);
- re-marshals the queue JSON;
- does all of this under the global `s.mu` on every item metadata update, finalize, and failure (`worker.go:859, 1215, 1546`).

#### Scope

- Add a `queue_items` table with a migration from the JSON column.
- Update only the changed item rows.
- Move persistence out of the global lock via a single writer goroutine or a per-job lock.

**Progress (2026-09-24):** Merged by [PR #55](https://github.com/ajbergh/yt-dl-go/pull/55) as `3a8e4b0`. Adds a transactional migration from the legacy queue JSON to normalized `queue_items`, updates queue/file/failure rows only when stored fields change, and routes job snapshots, settings, queue order, and deletion writes through a FIFO persistence writer that releases the global job mutex while SQLite runs. Shutdown drains the writer after HTTP handlers and workers stop. Follow-up fixes ensure retries are not schedulable before their save completes, repair a missing Library table even when the schema version advanced, and cache persisted queue/file/failure/Library signatures so no-op child-row writes are skipped. All nine final CI checks passed, including Go tests and race tests, golangci-lint, Go and JavaScript analysis, CodeQL, and Linux, Windows, and macOS builds.

### M1.3 Pagination, filtering, and indexes

**Status:** [x] Search, facets, whole-group pagination, and 10,000-item API characterization complete · **P1** · **Area:** API / persistence

The original unpaged `GET /api/jobs` and SSE snapshot return full active-job state and remain unchanged. The Library now has an opt-in cursor-paginated API with server-side search and facets; this milestone also records representative search and page costs at 10,000+ files.

#### Scope

- Cursor-paginated `GET /api/library` with server-side search, sort, and category/channel/type facets.
- Indexes on `created_at`, `status`, category, and channel.
- Move the search index from the frontend (`home.tsx:147`) to SQLite, e.g. FTS5 (supported by `modernc.org/sqlite`).

**Implementation note (2026-09-24):** Repository review confirmed that `/api/jobs` and SSE payloads feed the complete active queue and should remain unchanged in this slice. At the time, `/api/library` loaded the full durable result set and computed facets/search in the browser. Its result rows are grouped by source job, so pagination must select job groups first and then load every file for each selected job. Preserve newest-source-job ordering with a stable cursor and define global totals/facets separately from page contents. Existing indexes are `queue_items(job_id, position)` and `library_items(source_job_id, source_item_index)`; category/channel search fields were stored inside JSON and needed indexed columns or a search table. Free-text substring matching is not equivalent to FTS token search, so keep that behavior explicit when designing the migration/API.

**Progress (2026-09-24):** The first backend pagination slice merged in [PR #57](https://github.com/ajbergh/yt-dl-go/pull/57) as `53212c4`. It adds opt-in `limit`/opaque cursor pagination to `GET /api/library`, with stable `(created_at, job_id)` ordering and page selection at the job-group level so chapter/multi-file downloads stay whole. The response includes `totalJobs` and `nextCursor`; the existing no-parameter `{jobs:[...]}` response remains unchanged. Migration v22 adds indexes for Library page order, category, and channel. Equal-timestamp cursor progression, complete multi-file groups, and invalid pagination input are covered. [PR #58](https://github.com/ajbergh/yt-dl-go/pull/58) merged the server-side substring search, type/category/channel filters, global facet and storage-stat metadata, and migration v23 predicate indexes. Frontend integration merged in [PR #59](https://github.com/ajbergh/yt-dl-go/pull/59) as `21ce328`: the Library requests filtered cursor pages, uses server-provided global facets and statistics, and offers a load-more control. Search/category/channel/type changes reset to the first server page; local filtering responds immediately while the server query loads and supports legacy `{jobs}` responses. Search-index evaluation: SQLite FTS5's trigram tokenizer supports substring candidates instead of token-only matching, but full-text queries shorter than three Unicode characters produce no matches. A compatible index can use trigram matches only to narrow candidates for queries of at least three Unicode characters, retain the current `instr(lower(...), ?)>0` predicate as the final compatibility check, and keep that exact predicate as a fallback for shorter queries. Do not enable diacritic removal or use tokenizer matches as the result semantics. The pinned `modernc.org/sqlite` v1.59.0 source includes the FTS5 trigram tokenizer. [PR #63](https://github.com/ajbergh/yt-dl-go/pull/63) merged as `1300464`: migration v25 adds trigger-maintained FTS5 trigram indexes for source metadata and file JSON; matches narrow candidates only, while the existing substring predicate remains authoritative and searches shorter than three Unicode characters use the scan path.

`perf/library-10k-measurement` merged in [PR #71](https://github.com/ajbergh/yt-dl-go/pull/71) as `6239dc2`. `BenchmarkLibraryAPIAt10100Files` in `src/server/library_scale_test.go` characterizes the public API over 10,100 durable files (101 source groups × 100 files) with no finished jobs loaded in memory. Command: `go test -run '^$' -bench '^BenchmarkLibraryAPIAt10100Files$' -benchmem -benchtime=20x`. On Windows/amd64, Go 1.26.8, AMD Ryzen 5 3600 6-Core Processor, the run reported first page of 100 groups (10,000 files): 626,683,115 ns/op, 52,657,444 B/op, 512,885 allocs/op; substring search returning one full group (100 files): 240,416,005 ns/op, 830,235 B/op, 12,384 allocs/op. This is a single local characterization, not an SLA. All PR checks passed, including Go/race/source validation, frontend integration and browser E2E, lint/vet, CodeQL, and Linux, Windows, and macOS builds.

### M1.4 Stable default data directory

**Status:** [x] Merged by PR #65 as `28dd800` · **P1** · **Area:** backend / UX

`DATA_DIR` defaults to `./downloads` relative to the current working directory (`main.go:80`). Launching from a different folder, a shortcut, or a terminal gives an empty library.

#### Scope

- Default to a per-user application directory: `%LOCALAPPDATA%\yt-dl-go`, `~/Library/Application Support/yt-dl-go`, or `$XDG_DATA_HOME/yt-dl-go`.
- Keep `DATA_DIR` as an override.
- Detect a legacy `./downloads/state.db` and offer a one-time migration.

**Progress (2026-09-24):** Merged in [PR #65](https://github.com/ajbergh/yt-dl-go/pull/65) as `28dd800`. The per-user default and `DATA_DIR` override are now documented at repository and server level. The explicit copy-based migration snapshots SQLite, copies managed data to staging, rebases job/resume paths (including the original pre-v16 schema), resolves existing parent symlinks before checking source/destination overlap, preserves the original legacy tree, and refuses to overwrite a destination containing user data. All PR CI checks passed, including race validation and platform builds.

### M1.5 Startup resilience, integrity, and backup

**Status:** [~] Startup integrity preflight merged; automatic migration backups in review; quarantine, export/import, and Windows ACL validation remain · **P1** · **Area:** persistence

One malformed JSON row in `queue_items` or `subtitle_json` makes startup fail fatally with "cannot load download history" (`store.go:798-800, 887-890`; `main.go:208-211`). There is no integrity check or backup.

#### Scope

- Run `PRAGMA quick_check` at startup.
- Quarantine unparseable rows instead of aborting, and surface them in diagnostics.
- Automatically back up the database before each migration using `VACUUM INTO`.
- Add a user-triggered **Export library** (DB plus a metadata JSON/CSV) and an **Import**.
- Set the pragmas through DSN `_pragma=` parameters so they survive connection recycling (`store.go:55-61`).
- Check `DATA_DIR` ACLs on Windows; today it is skipped (`main.go:169`).

**Progress (2026-09-24):** M1.1 and M1.3 are complete. Repository review found that legacy migration v21 and current queue/file loaders abort on malformed JSON, while silently skipping rows could let later saves erase evidence. The startup integrity preflight merged in [PR #72](https://github.com/ajbergh/yt-dl-go/pull/72) as `f0e79fc`: SQLite pragmas now live in the encoded DSN and run on recycled connections; `PRAGMA quick_check` runs before schema initialization/migrations and returns a typed error for detected integrity failures. Regression coverage checks connection recycling, a damaged table constraint, and Windows extended-length database paths. All PR checks passed, including Go/race/source validation, frontend integration and browser E2E, lint/vet, CodeQL, and Linux, Windows, and macOS builds.

`feat/migration-snapshots` implements one v1 snapshot for a fresh database and a `VACUUM INTO` snapshot immediately before each pending migration on an existing database. Failed snapshots abort migration and incomplete `.partial` files are cleaned up; v20 repair also restores the version-appropriate Library indexes and FTS triggers. Focused snapshot/integrity tests and `go test ./... -count=1` pass locally; PR review and CI are pending. Quarantine/diagnostics, export/import, and Windows ACL validation remain open.

### M1.6 Table-driven migrations with fixture tests

**Status:** [ ] · **P2** · **Area:** maintainability

Migrations v2–v15 are 14 near-identical copy-pasted functions and call blocks, about 375 lines (`store.go:123-557`). v1 runs outside a transaction.

#### Scope

- Replace them with a `[]migration{version, stmts}` table run in transactions.
- Add golden fixture DBs at v1, v6, v10, and v14, each tested to migrate to head.
- Remove the write-only `config` table (`store.go:566-587`) and the write-only `download_parts` table (see M2.3), or wire them up.

### M1.7 Move desktop-relevant settings into the UI

**Status:** [ ] · **P2** · **Area:** config

`RETENTION`, `MAX_JOB_BYTES`, `JOB_TIMEOUT`, and `CHROME_PATH` are environment-only (`main.go:78-148`), and the per-process transfer slot count is hard-coded at 4 (`main.go:189`). Desktop users should not need environment variables.

#### Scope

- Persist these settings in `app_settings` with environment-variable overrides, and show which source is in effect in Settings.
- Optionally support a `config.toml` in the data directory for headless use.

---

# Milestone 2 — Download engine reliability

Goal: no hung slots, no silent corruption, no avoidable restarts from zero.

### M2.1 Stall detection and per-item timeouts

**Status:** [x] Merged by PR #35 · **P0** · **Area:** engine

- `JOB_TIMEOUT` is now an optional overall job cap and defaults to disabled. Playlist and item metadata retain bounded lookup deadlines.
- Native progressive/audio reads and adaptive range bodies close after 60 seconds without data and return retryable `errRead`; the watchdog runs before bandwidth throttling.
- Each media transfer has a size-, duration-, and bandwidth-aware deadline (15-minute minimum, seven-day cap). Long playlists are not limited by one shared six-hour deadline unless an overall cap is configured.

#### Scope

- [x] Add a per-read idle deadline; 60 seconds without data closes the reader and returns retryable `errRead`.
- [x] Add per-item metadata and transfer deadlines, with transfer time derived from expected bytes, media duration, and the configured bandwidth limit.
- [x] Turn `JOB_TIMEOUT` into an optional overall cap; `none` and `0` disable it.
- [x] Add bounded exponential backoff with jitter between adaptive range retries; retain retries at three attempts.

**Progress (2026-09-23):** Merged by [PR #35](https://github.com/ajbergh/yt-dl-go/pull/35) (`58679fd`) after the Go test/lint/vet checks, frontend browser end-to-end tests, CodeQL, and Linux, Windows, and macOS builds passed. Native read watchdogs cover progressive video/audio sources and adaptive HTTP ranges, item deadlines bound transfers, and adaptive range attempts back off with jitter. `JOB_TIMEOUT` is optional and defaults to `none`.

### M2.2 Atomic, durable finalization everywhere

**Status:** [x] Merged by PR #36 · **P0** · **Area:** engine / file safety

Before M2.2, adaptive MP4/WebM outputs were renamed without `Sync`, `publishOutput` wrote directly to final user-visible names, and several paths reused existing files without verifying their size.

#### Scope

- Write every output as `.part`, then `fsync` → `close` (error checked) → `rename` → `fsync` the directory.
- Publish through a temporary name in the destination folder.
- Always verify the expected size before reusing an existing final file.
- `published-only` should `rename` when source and destination are on the same volume instead of copying then deleting multi-GB files (`output.go:209`, `worker.go:1160`).

**Progress (2026-09-23):** Merged by [PR #36](https://github.com/ajbergh/yt-dl-go/pull/36) (`a1bc45c`) after the Go tests, race tests, browser end-to-end checks, CodeQL, and Linux, Windows, and macOS builds passed. Media and caption outputs now finalize from temporary names with checked sync/close and durable rename/publication. Existing transfer files are reused only when a known expected size matches; adaptive mux outputs are rebuilt because their final size is unknown until muxing completes. `published-only` moves the managed file into the destination directory when possible, with a synced copy fallback for cross-volume moves. POSIX directory entries are synced; Windows uses `MoveFileExW` with `MOVEFILE_WRITE_THROUGH` because directory handles cannot be synced there.

### M2.3 Resume on every transfer path

**Status:** [x] Merged by PR #37 · **P1** · **Area:** engine

Before this work, only native adaptive ranges resumed. Progressive video, M4A, and MP3 source streams restarted from zero, and `download_parts` was write-only. Adaptive resume trusted `.part` size without validating source identity or acquisition method.

#### Scope

- [x] Use HTTP Range resume for progressive and audio streams when the source has a known length and a resolvable media URL.
- [x] Use per-track `download_parts` checkpoints to validate itag, expected length, source fingerprint, path, and acquisition method before appending.
- [x] Confirm and prevent the browser SABR crash-recovery hazard: tag browser parts and discard incomplete browser-acquired bytes instead of appending native ranges.

**Progress (2026-09-23):** Merged by [PR #37](https://github.com/ajbergh/yt-dl-go/pull/37) (`29b745f`) after the full source-validation rerun, Go and frontend analysis, CodeQL, and Linux, Windows, and macOS package builds passed. The first race-test run hit a timeout in the existing cancellation-queue test; a second full source-validation run and the rerun both passed, and the focused test passed locally.

### M2.4 Deterministic output naming and richer tokens

**Status:** [x] Merged by PR #38 · **P1** · **Area:** engine / output

Naming tokens are replaced by iterating a Go map (`output.go:165-169`), and map order is random. A title containing `{channel}` or `{resolution}` can therefore produce different filenames from run to run.

#### Scope

- Do a single-pass tokenizer with a fixed order that never re-expands substituted values.
- Add tokens: `{id}`, `{upload_date}`, `{playlist}`, `{index}`, `{ext}`, `{fps}`, `{codec}`.
- Show a live filename preview in Settings.
- Make output file and folder permissions configurable. Today they are `0600`/`0700` (`output.go:187, 202`), which can hide media from Plex, Jellyfin, or other users on Linux/macOS.

**Progress (2026-09-23):** Merged by [PR #38](https://github.com/ajbergh/yt-dl-go/pull/38) (`3029525`) after two full source-validation runs, Go/frontend analysis, CodeQL, and Linux, Windows, and macOS builds passed. The backend uses a single-pass tokenizer for all 11 tokens; substituted title/channel text is never re-expanded. The Settings preview uses matching token expansion, sanitizing, and extension behavior. Schema migration v17 persists published file/folder modes in both settings and queued jobs; defaults remain private (`0600`/`0700`), and the modes apply to published media, captions, and destination directories. Local browser E2E and Bun unit execution were unavailable because Chrome/Chromium and Bun are not installed; CI ran both.

### M2.5 Bound SABR capture memory

**Status:** [x] Merged by PR #39 · **P1** · **Area:** engine

Out-of-order SABR segments accumulate in `capture.ready` with no aggregate limit (`sabr.go:385`). The budget is checked per segment (`sabr.go:298`) and at write time (`sabr.go:450`), not across the buffer, so a missing first sequence can hold a whole track in RAM.

#### Scope

- Cap the ready-buffer size in bytes, spilling to disk or failing over to the range path.
- Re-evaluate `maxSABRParts = 10000` (`sabr.go:25`) for multi-hour 4K responses.

**Progress (2026-09-23):** Merged by [PR #39](https://github.com/ajbergh/yt-dl-go/pull/39) (`ee6f120`) after two complete source-validation runs, Go/frontend analysis, CodeQL, and Linux, Windows, and macOS package builds passed. Out-of-order `capture.ready` bytes are capped at 64 MiB; overflow fails browser capture so existing cleanup can fall through to native HTTP ranges. Byte accounting is released on ordered flush and reset with short-candidate state. The UMP part-count guard is 100,000; the 32 MiB per-part and 128 MiB browser response limits remain. Tests cover cap rejection, flush/reset accounting, range fallback, and parser boundaries. This limit covers `capture.ready`, not all process memory used by the browser response buffer or a fragment currently being assembled.

### M2.6 Higher-quality audio for adaptive MP4

**Status:** [x] Merged by PR #40 · **P1** · **Area:** engine / quality

Adaptive H.264 MP4 output takes its AAC track from the progressive itag-18 stream, about 96 kbps (`worker.go:1811-1814`). That is a documented workaround for mid-range rejection of adaptive AAC URLs. Meanwhile, the channel count is taken from `selection.audio` (`worker.go:2235`).

#### Scope

- Try itag 140 (or the best AAC) through the browser/range path first, and fall back to itag 18 with a job note.
- Take the audio metadata from the track actually muxed.
- Add a test for channel-count consistency.
**Progress (2026-09-24):** Merged by [PR #40](https://github.com/ajbergh/yt-dl-go/pull/40) (`0d637bb`). The selected adaptive AAC is tried through browser capture/native ranges first; only retryable read failures (or unknown AAC length) select the verified H.264/AAC progressive fallback, with a job note. Mux metadata follows the source actually downloaded, and the budget reserves the larger known source. Selection, preferred/fallback acquisition, non-read error handling, mux metadata, budget, and browser AAC policy are covered. Go tests, race tests, vet (including E2E tag), frontend checks, CodeQL, browser E2E, and Linux/Windows/macOS package builds passed. One initial race run hit the existing 15-second cancellation-test timeout; the repeated full validation passed.

### M2.7 Browser session pooling and event-driven readiness

**Status:** [x] Merged by PR #41 · **P1** · **Area:** engine / performance

- `browserFactory` is called per adaptive item (`worker.go:1971, 2106`). Each item owns a Chrome process. `Prepare` has 8 seconds of fixed sleeps plus nested JavaScript timers (`browser_provider.go:195-238`), so up to 6 items can each start a Chrome and wait serially.
- A capture has a byte-aware deadline capped at 45 minutes and may be retried twice per track; serial MP4 video/audio can repeat that path for each track before native fallback.

#### Scope

- Keep one long-lived headless browser per process with one tab per item, shut down after idle.
- Replace sleeps with player-state/CDP events.
- Cap browser-path time per item (e.g. a few minutes of no progress means fall back).
- Deduplicate `CaptureTrack`/`CaptureTracks` (about 150 duplicated lines, `browser_provider.go:229-531`).

**Progress (2026-09-24):** Merged by [PR #41](https://github.com/ajbergh/yt-dl-go/pull/41) (`4a1134d`). The service now owns one pooled Chrome process with independent per-item tab leases, two-minute idle shutdown, and service-shutdown cleanup. Preparation's fixed sleeps and nested timers are replaced by bounded metadata and playing-state waits. Single- and dual-track capture share setup, attachment, progress/stall monitoring, keep-alive, completion, and cleanup logic. The one-minute no-progress timeout remains enforced per browser attempt (two attempts maximum per track); byte-aware transfer deadlines remain for active captures. Six-way lease isolation, idle restart/shutdown, Go/race tests, vet, browser E2E, CodeQL, and Linux/Windows/macOS package builds passed. An initial lint run identified an obsolete constructor, which was removed before the green reruns.

### M2.8 Extraction resilience against YouTube changes

**Status:** [~] Core merged by PR #42; synthetic drift-matrix follow-up underway on `roadmap/m2-8-drift-coverage`; captured samples remain open · **P1** · **Area:** engine / resilience

- Native extraction uses `kkdai/youtube/v2`; before M2.8 it was forced to `AndroidClient`, and range requests hard-coded that profile's User-Agent and Origin. One upstream break could stop every download.
- Browser preparation now uses player API calls and no longer depends on the Settings/Quality DOM text; consent dismissal still checks English labels (`browser_provider.go:199-208`).
- SABR/UMP decoding uses hand-coded protobuf field numbers (`sabr.go:16-20, 736-767`).

#### Scope

- Put an `extractor` interface in front of kkdai with ordered supported client profiles (Android → iOS → embedded-player → web) and automatic fallback. The pinned dependency exports no TV client profile; do not invent one without a verified implementation.
- Run a startup/self-test capability probe and report it in `/api/health`.
- Make browser automation locale-independent (use `hl=en` or player API calls instead of DOM text).
- Build a recorded-fixture corpus of player responses and UMP streams, with drift tests that fail loudly when the format changes.
- Keep kkdai upstream releases monitored through Dependabot (weekly Go-module updates are already configured).

**Progress (2026-09-24):** The extractor profiles, serialized profile selection, profile-matched stream/range requests, safe resume validation, local health probe, and `hl=en` browser locale merged as [PR #42](https://github.com/ajbergh/yt-dl-go/pull/42) (`79c4405`). CI passed Go/race tests, lint, dependency/security scans, vet, browser E2E, CodeQL, and Linux/Windows/macOS builds. CI initially caught a Staticcheck warning in the fixture transport; the assignment was removed and the rerun passed. [PR #43](https://github.com/ajbergh/yt-dl-go/pull/43) merged as `3d264cb`, adding a checked-in minimal UMP parser/assembly regression fixture and provenance for both fixtures. The player and UMP fixtures are synthetic deterministic inputs, not captured YouTube responses or playable media; see `src/server/testdata/youtube/PROVENANCE.md`. PR #43's Go/race tests, lint, dependency/security scans, vet, browser E2E, CodeQL, and Linux/Windows/macOS builds passed; an initial duplicate browser E2E job timed out during Chrome startup and its rerun passed. Synthetic drift coverage for progressive/adaptive formats, bitrate ordering, optional/unknown fields, empty-format rejection, UMP parser parity, unknown protobuf fields, and time-range data merged in [PR #50](https://github.com/ajbergh/yt-dl-go/pull/50) (`8f805c1`); Go/race validation, lint, static analysis, CodeQL, and Linux/Windows/macOS builds passed. Captured, redistributable player/UMP samples remain open before M2.8 can be marked complete. Weekly Go Dependabot already covers the dependency.

### M2.9 Bandwidth limiter improvements

**Status:** [~] · **P2** · **Area:** engine

- Any bandwidth limit turns off browser HD capture entirely (`worker.go:1773, 1891`).
- The limiter polls every 5 ms (`bandwidth.go:281`).
- Changing from unlimited to limited mid-transfer is ignored, because the reader is chosen when the transfer starts (`bandwidth.go:342`).

#### Scope

- [x] Always wrap readers so limit changes apply live.
- [x] Replace polling with timer-based waits.
- [x] Investigate CDP `Network.emulateNetworkConditions` so browser capture can stay on under a limit.

**Progress (2026-09-23):** Live native reader wrapping and timer-based FIFO waits merged in [PR #44](https://github.com/ajbergh/yt-dl-go/pull/44) (`22f4634`). Limit changes wake queued transfers immediately; wait timers target a useful chunk rather than polling, and context cancellation still removes waiters. Regression coverage exercises a reader created while unlimited, immediate release when switched back to unlimited, and sub-nanosecond delay progress. CDP network emulation is available through the leased target's scoped CDP context, but its download limit is per target. Applying the configured server-wide cap independently to multiple browser targets would permit aggregate throughput above the setting, and native transfers would remain outside that CDP accounting. Browser capture therefore remains disabled while a cap is active until a shared arbiter can account for browser and native traffic together. The full Go suite, lint, vet, race suite, CodeQL, and Linux/Windows/macOS builds all passed in PR #44 CI; the local Windows race run requires cgo, which is disabled in this environment.

### M2.10 Outbound proxy support

**Status:** [~] · **P2** · **Area:** network

The media transport sets `Proxy: nil` (`network.go:137`), so users behind a corporate or HTTPS proxy cannot download at all. The update client, by contrast, uses the default proxy-aware transport.

#### Scope

- [x] Add opt-in `HTTPS_PROXY` support for native YouTube metadata, media, thumbnail, and caption requests. Accept HTTP or HTTPS CONNECT proxies from the environment; do not persist proxy credentials in Settings or SSE payloads.
- [x] Resolve and validate all target addresses before CONNECT, send CONNECT to a checked public IP, and retain the original hostname for TLS SNI and certificate validation.
- [ ] Decide whether browser-assisted Chrome downloads also need proxy support; Chrome currently follows its own direct networking path.

**Progress (2026-09-23):** Native `HTTPS_PROXY` support merged in [PR #45](https://github.com/ajbergh/yt-dl-go/pull/45) (`1c1afa8`). `HTTPS_PROXY` and lowercase `https_proxy` accept HTTP or HTTPS proxy URLs. Native requests resolve every target address and reject private, reserved, or mixed answers before contacting the proxy; CONNECT targets a checked public IP, then TLS validates the original hostname. Proxy credentials remain only in the environment and CONNECT header. The README documents coverage and the current Chrome limitation. The strict pre-CONNECT validation avoids letting the proxy resolve an unchecked target, while the explicit proxy endpoint itself is trusted configuration and may be private. Proxy/guard regression tests, the full Go suite, `go vet`, `golangci-lint`, race checks, CodeQL, and Linux/Windows/macOS builds passed. Chrome-assisted traffic still follows its direct networking path; decide whether to include it before marking M2.10 complete.

---

# Milestone 3 — Media and metadata features

### M3.1 Metadata for MP4, M4A, and WebM

**Status:** [x] Merged by PR #46 (`17f0bb0`) · **P1** · **Area:** engine / media

MP3, MP4/M4A, and WebM outputs now receive supported metadata and cover art.

#### Scope

- [x] MP4/M4A `moov/udta/meta/ilst`: title, artist, album, date, comment/source URL, and JPEG/PNG `covr`.
- [x] WebM `Tags` plus a cover attachment.
- [x] Pure Go only.

**Progress (2026-09-24):** Merged by [PR #46](https://github.com/ajbergh/yt-dl-go/pull/46) (`17f0bb0`). CI passed on Linux, Windows, and macOS, including race tests, CodeQL, and production builds. MP4/M4A metadata and WebM tags/attachments are rewritten atomically with pure Go; unsupported fragmented MP4 and over-budget rewrites preserve the original media.

### M3.2 Chapters

**Status:** [~] MP3 chapter splitting merged; M4A/caption handling remains · **P1** · **Area:** engine / media

The current metadata pipeline has no chapter model. YouTube chapter timestamps are available in the description, but parsing, Library persistence, container writing, and preview navigation need to be added.

#### Scope

- [x] Parse chapters from video metadata and store them with the Library item.
- [x] Write MP4 Nero `chpl` atoms and WebM `Chapters`.
- [x] Show chapters in the preview player.
- [~] Offer opt-in MP3 chapter splitting; M4A splitting and chapter-clipped caption sidecars remain open.

**Progress (2026-09-24):** Chapter parsing, Library/SQLite persistence, MP4/WebM chapter metadata, and preview navigation merged by [PR #47](https://github.com/ajbergh/yt-dl-go/pull/47) (`15f9369`). Opt-in MP3 chapter splitting merged by [PR #48](https://github.com/ajbergh/yt-dl-go/pull/48) (`8ae44e0`): one AAC decode pass writes individually tagged chapter MP3s, queue items expose the grouped file IDs for ZIP save, and grouped outputs persist through SQLite and restart normalization. The one-file default and original M4A path remain unchanged; M4A splitting and chapter-clipped captions remain open scope. Full CI passed on the merged PR, including Go/race tests, Bun UI tests, browser E2E, lint, vulnerability scanning, vet, CodeQL, and Linux/Windows/macOS builds.

### M3.3 Embedded subtitles and thumbnails

**Status:** [ ] · **P2** · **Area:** engine / media

Subtitles are sidecar-only, and thumbnails are embedded only in MP3.

#### Scope

- Optionally embed captions as an MP4 `tx3g`/`wvtt` track or a WebM WebVTT track.
- Embed cover art in every container (depends on M3.1).
- Offer **Publish sidecar artwork** (`cover.jpg` / `<name>.jpg`), which was deferred in v1 P1.1.

### M3.4 Opus audio passthrough and more audio formats

**Status:** [ ] · **P2** · **Area:** engine / media

`selectAudioFormat` is AAC-only (`worker.go:393`), even though Opus is usually YouTube's best audio. MP3 conversion is lossy-to-lossy from a roughly 128 kbps source and runs single-threaded (`audio.go:82-106`).

#### Scope

- **Audio only → Opus · original**: remux the WebM Opus stream to `.opus`/`.webm` with no transcode.
- Show a UI hint that M4A/Opus originals beat MP3 for quality.
- Parallelize or stream MP3 encoding so it does not need a full source download first.

### M3.5 Bulk retry and queue editing

**Status:** [~] Implementation in review · **P1** · **Area:** backend / UX

- Retry works one item at a time (`retry_item.go:47-49`), and the UI's batch actions send N sequential requests (`use-jobs.ts:283-288`).
- `dev_mock_new_ui` shows changing quality or format while an item is queued or paused.

#### Scope

- Add **Retry all failed items** in a job.
- Add bulk endpoints: pause/resume/cancel/delete for a set of job IDs.
- Allow editing quality, format, and category for queued or paused jobs.
- Add an add-as-paused / **Start immediately** toggle at queue time (from the mock).

### M3.6 Scheduled and watched sources

**Status:** [ ] · **P3** · **Area:** product

These features are only for content the user owns or is permitted to download.

#### Scope

- Download windows (e.g. only between 01:00 and 06:00).
- Watched playlists/channels: periodically re-inspect a saved playlist URL and queue only new items, with explicit per-source opt-in and rights confirmation.
- Depends on M1.1 so already-downloaded items can be deduplicated by video ID.

### M3.7 Duplicate detection

**Status:** [ ] · **P2** · **Area:** product

#### Scope

- Warn at inspect time when a video ID/format combination already exists in the Library.
- Offer Skip, Download anyway, or Replace.
- Default playlist behavior: skip items already in the Library.

---

# Milestone 4 — Frontend architecture, UX, and accessibility

### M4.1 Real routes and URL state

**Status:** [ ] · **P1** · **Area:** frontend

React Router has a single route (`routes.tsx:16-18`), and tabs are a `useState` (`home.tsx:35`). Refreshing the page resets to Queue, Back leaves the app, and nothing can be deep-linked.

#### Scope

- Make `/queue`, `/library`, `/settings`, and `/library/:id` real routes, with filter and search state in the query string.
- Lazy-load Settings and the dnd-kit queue surfaces.

### M4.2 Replace prop drilling with a store

**Status:** [ ] · **P1** · **Area:** frontend

`HomePage` owns about 20 `useState` hooks and passes setters between hooks (`home.tsx:52-80`). `QueuePage` takes 47 props (`queue.tsx:17-64`), `LibraryPage` 21, and `SettingsPage` 20.

#### Scope

- Add a small external store (`useSyncExternalStore` or Zustand; the lint config already expects `@/lib/store`).
- Keep jobs in a `Map` keyed by ID, with selectors.
- Track pending state per action and per job instead of a single `busyAction` string (`use-jobs.ts:48`).

### M4.3 Keep unsaved settings separate from server state

**Status:** [ ] · **P1** · **Area:** frontend / correctness

`settings-changed` and `snapshot` SSE events overwrite the object the Settings form is editing (`use-service.ts:174-186`), so unsaved edits disappear silently. The `settings-changed` merge also bypasses `hydratedSettings`.

#### Scope

- Keep separate draft and server state.
- Show a dirty indicator and an "unsaved changes" guard when navigating away.
- On an external change, prompt the user instead of overwriting their draft.

### M4.4 Render performance and lightweight progress events

**Status:** [ ] · **P1** · **Area:** frontend + backend

- Every `job-progress` event carries a full job snapshot with all items, up to 5 Hz per active item (`worker.go:1069-1073`, `events.go:77`).
- Each event triggers `setJobs` and re-renders all of `HomePage`, recomputing about 10 O(n) derivations. There are no `memo`/`useCallback` uses, and `filteredQueue` is not memoized (`home.tsx:302`).
- Thumbnails are fetched again as blobs on every Library visit (`view-model.tsx:162-182`) without `loading="lazy"`.

#### Scope

- **Backend:** send item-level progress deltas (`jobId`, `index`, bytes, speed, eta). Emit an explicit `resync` event when a slow client drops events, and support `Last-Event-ID` (event IDs currently reset on restart and dropped events are silent, `events.go:47-71`).
- **Frontend:**
  - Coalesce updates per animation frame.
  - Memoize rows.
  - Virtualize long queue and Library lists; a 151-item playlist renders 151 rows with about 6 buttons each.
  - Collapse playlist groups by default.
  - Serve thumbnails through cacheable `<img>` URLs with lazy loading.
  - Defer search with `useDeferredValue`.

### M4.5 Input ergonomics

**Status:** [ ] · **P1** · **Area:** UX

- Pressing Enter in the URL field submits the *add* form (`queue.tsx:85,101`) and shows "Confirm you have permission…" instead of inspecting. This is a real usability bug.
- There is no paste detection and no URL drag-and-drop, and the only keyboard handler is at `settings.tsx:134`.

#### Scope

- Enter inspects.
- Paste a YouTube URL anywhere to auto-inspect.
- Drag and drop links and `.txt` URL lists.
- Shortcuts: `/` focuses search, Esc closes, Ctrl/Cmd+V paste-inspects, and `?` shows shortcut help.

### M4.6 Feedback: toasts, undo, bulk Library actions

**Status:** [ ] · **P1** · **Area:** UX

- The notice banner never auto-dismisses (`home.tsx:352`).
- Destructive actions use `window.confirm` and have no undo (`use-jobs.ts:63-67`).
- The Library has no bulk selection or sort control.

#### Scope

- A toast queue.
- A soft-delete undo toast for **Remove from Library**, with the actual deletion delayed.
- Library checkboxes with bulk remove, ZIP, and move-category.
- Sorting by date, size, title, and channel.
- An in-app confirmation dialog that lists exactly which copies will be deleted.

### M4.7 Accessibility pass

**Status:** [ ] · **P1** · **Area:** a11y

- The preview modal (`home.tsx:453-462`) has `role="dialog"` but no Esc, focus trap, initial focus, focus restore, or scroll lock. Fix by using a native `<dialog>` with `showModal()`.
- Progress bars are bare `div`s (`queue.tsx:322`). Add `role="progressbar"` with `aria-valuenow/min/max` and a text alternative.
- Contrast is too low: `text-neutral-600` on `#0c0d10` is about 2.6:1, and `text-neutral-500` appears at 49 sites, many at `text-[10px]`/`text-[11px]`. Raise to AA (neutral-400 or lighter) with a 12px minimum.
- Motion ignores user preferences. Add `motion-reduce:` variants and use non-smooth scrolling when reduced motion is requested.
- Add `aria-invalid` and `aria-describedby` on form fields that have errors.
- Add axe-core checks to the browser E2E suite.

### M4.8 Theming and light mode

**Status:** [ ] · **P2** · **Area:** UX

- Dark mode is hard-coded (`home.tsx:312`, `bg-[#0c0d10]`), even though `index.html:9` declares `color-scheme: light dark`.
- The not-found page renders with different, light tokens.
- `index.css:45-190` contains unused shadcn sidebar and chart tokens.

#### Scope

- Use a token-driven theme with system/light/dark options.
- Remove the unused tokens.

### M4.9 Features from the design mock not yet in production

**Status:** [ ] · **P2** · **Area:** UX

From `dev_mock_new_ui`, prioritized:

1. Estimated file size per quality option, and fps/HDR badges.
2. Live total download speed in the header.
3. Free-disk-space indicator (needs a new Go disk-free API; see M5.4).
4. A target-path preview on each queue item.
5. A Library sidebar grouped by channel or category.
6. Hover-to-play thumbnail cards with duration and format badges.
7. A metadata inspector in the player modal.
8. "Copied" and "Saved" confirmations (covered by the M4.6 toasts).

### M4.10 Frontend code quality

**Status:** [ ] · **P2** · **Area:** maintainability

- Add Prettier. JSX lines currently run 300–700+ characters in `queue.tsx`, `library.tsx`, and `settings.tsx`.
- Split the pages into components (`DraftCard`, `QueueCard`, `LibraryCard`, …) and move non-component exports out of `view-model.tsx`; 19 react-refresh warnings come from mixing them.
- Remove duplicated helpers:
  - the video-ID regex (`downloader.ts:194`, `view-model.tsx:114`);
  - the playlist-selectable predicate (three places);
  - the bitrate list, the ticket-path regex, and category resolution;
  - the nine near-identical `setDrafts(map(...))` updaters (`queue.tsx:152-191`).
- Rename `isActive`, which confusingly includes `queued` (`downloader.ts:353`).
- Remove dead code: `serviceURL`, `ServiceConnection.token` (after M5.1), `durationMetric`, and the unused `finishedFiles`/`queueJobs`.
- Prune the unbounded `clearedQueueItems` localStorage entry (`use-jobs.ts:293`).
- Remove the template restrictions and nonexistent paths from `eslint.config.js:38-106`.

### M4.11 Internationalization readiness

**Status:** [ ] · **P3** · **Area:** UX

Move all UI strings into a message catalog and make `formatBytes` and date formatting locale-aware. Translation itself remains optional.

---

# Milestone 5 — API, security model, and observability

### M5.1 Built-in UI authentication (token by default)

**Status:** [!] needs design review · **P1** · **Area:** security

- The API supports bearer tokens, but `builtInServiceConnection()` hard-codes `token: ""` (`view-model.tsx:244`), and the README says to leave `API_TOKEN` unset when using the UI.
- As a result, on loopback any local process or other OS user can drive the API, including the native folder picker and file reveal.

#### Proposal

- Generate a random per-launch token.
- The auto-opened browser URL carries it in the fragment (`/#t=…`). The UI moves it into `sessionStorage` or exchanges it for an `HttpOnly; SameSite=Strict` session cookie, then strips it from the URL.
- `--background` mode prints the tokenized URL to the console.
- Keep `API_TOKEN` as a fixed-token override for external clients.
- Require the token for every route except static assets and file tickets.

### M5.2 Standard router, versioning, and error codes

**Status:** [ ] · **P2** · **Area:** API

- `ServeHTTP` is a 300-line hand-rolled string-splitting router (`server.go:305-607`).
- There is no API versioning.
- Errors are prose-only `{"error": "..."}` (`server.go:214-216`), and 405 responses lack an `Allow` header.

#### Scope

- Move to Go 1.22+ `http.ServeMux` method/path patterns.
- Add an `/api/v1` prefix, keeping `/api` as an alias for a deprecation window.
- Return RFC 9457 problem details with stable machine-readable `code` values.
- Remove the redundant `items`/`entries` pair from the inspect response (`api_extra.go:131-134`).

### M5.3 OpenAPI contract and generated TypeScript types

**Status:** [ ] · **P2** · **Area:** API / frontend

- The only API contract is the README tables.
- TypeScript types mirror untyped Go strings by hand (`downloader.ts:6`, `store.go:24-37`).
- The quality list is duplicated in four places (`server.go:674, 770`, `api_extra.go:95`, `view-model.tsx:35-42`).
- Default settings are duplicated in `use-service.ts:12-25`.

#### Scope

- Define typed Go enums.
- Maintain an OpenAPI spec (generated or hand-written with a contract test).
- Generate TypeScript types (`tygo` or `openapi-typescript`).
- Add a CI diff check.

### M5.4 Structured logging, diagnostics, and a real health endpoint

**Status:** [ ] · **P1** · **Area:** observability

- Logging is unstructured `log.Printf` to stderr only (`main.go:268`).
- SABR per-header debug lines log unconditionally (`sabr.go:271, 285`).
- `/api/health` reports `ready = engine != nil` and always returns an empty `missing` (`server.go:330-338`).

#### Scope

- Use `log/slog` with levels and a `LOG_LEVEL` setting.
- Write a rotating log file under the data directory.
- Log requests at debug level.
- Health should report Chrome discovery and version, DB status, free disk space for the data and output directories, the extractor self-test (M2.8), and update-check status.
- Add **Settings → Diagnostics → Download support bundle**: a redacted ZIP with logs, effective config, DB stats, and versions. It must never include signed URLs or tokens.

### M5.5 Rate limiting and caching

**Status:** [ ] · **P2** · **Area:** API

- `/api/inspect` makes up to 6 YouTube calls per request (`api_extra.go:139-155`) and has no concurrency limit.
- `/api/update` calls GitHub on every request (`server.go:364`), against an unauthenticated limit of 60 requests per hour.
- SSE connections are unbounded.

#### Scope

- Add per-endpoint concurrency semaphores.
- Cache update results for about 6 hours.
- Cap concurrent SSE clients.

### M5.6 Hold the global lock only for in-memory work

**Status:** [ ] · **P1** · **Area:** backend / performance

The job-action switch in `ServeHTTP` holds `s.mu` across `RemoveAll` and DB writes (`server.go:454-455, 475-479`), which stalls every worker and SSE publisher while it runs.

#### Scope

Snapshot state under the lock, then do the I/O outside it, with per-job state machines. Folds into M1.2.

---

# Milestone 6 — Backend maintainability

### M6.1 Split `worker.go` and introduce internal packages

**Status:** [ ] · **P2** · **Area:** maintainability

`worker.go` has 2,355 lines, and the whole server is `package main`. The largest functions:

| Function | Lines | Location |
| --- | --- | --- |
| `selectFormatForStrategy` | 189 | `worker.go:204` |
| `transferAdaptiveWebM` | 160 | `worker.go:1846` |
| `transferAudio` | 149 | `worker.go:1243` |
| `processItem` | 145 | `worker.go:1076` |
| `run` | 132 | `worker.go:530` |

#### Scope

Extract in this order, with a pure move first and no behavior change:

1. Files: `formats.go`, `scheduler.go`, `progress.go`, `budget.go`, `transfer_*.go`, `mp4_mux.go`, `retention.go`.
2. Packages: `internal/media` (webm/mp4 mux, id3, audio), `internal/youtube` (extractor, SABR, browser), `internal/store`, and `internal/api`.

### M6.2 Remove duplication and dead code

**Status:** [ ] · **P2** · **Area:** maintainability

- The open-stream → close-once → length reconcile → `.part`/Sync/rename boilerplate is repeated five times (`worker.go:1282-1313, 1421-1452, 1601-1644, 2142-2185`). Replace it with one `fetchToFile` helper; this also delivers M2.2 consistently.
- `enqueueJobWithFallback` takes 14 positional arguments (`server.go:835`). Replace them with a `jobSpec` struct.
- Deduplicate the quality validation (three places), the token check (two), the "stopped and no readers" guard (four), and the `noteX` helpers (three).
- Delete production code that only tests reference: `OpenRange`, `capturePausedResponse`, `closeOrphanedResult`, `rangeURL`, and `sameBrowserRangeURL` (`browser_provider.go:731-1044`).
- Name the magic numbers as documented constants:
  - 6 workers per job;
  - 8 MiB chunks;
  - 100 empty reads;
  - 5s/8s sleeps;
  - 16× playback;
  - the 45-minute and 2 MiB/s caps;
  - the 1500 ms tolerance.

### M6.3 Injectable HTTP and extractor dependencies

**Status:** [ ] · **P2** · **Area:** testability

- The range HTTP client is built inline (`worker.go:2056`).
- `youtube.DefaultClient` is mutated globally (`native.go:19`).
- A new client and transport are created for every operation, so connections and player JS are not reused (`worker.go:110-115, 1095`).

#### Scope

Inject an HTTP doer and extractor. Share one transport per process. This enables the M7.4 tests.

---

# Milestone 7 — CI and test quality

### M7.1 Restructure the CI matrix

**Status:** [ ] · **P1** · **Area:** CI

- Each push performs about 6 UI builds and 6 Go test runs, because each package job rebuilds and retests (`build-windows.ps1:101`, `build-unix.sh:75-76`).
- The macOS amd64 binary is built on an arm64 runner and Linux arm64 is cross-built, so neither binary is ever executed.

#### Scope

- Build the UI once and pass it on as an artifact.
- Run the test matrix natively on ubuntu, windows, and macos-arm64.
- Cross-compile the other targets without retesting, plus a smoke run of `--version` under emulation or on a native arm64 runner.
- Cache Go modules and npm packages.

### M7.2 Go static analysis, race detection, and vulnerability scanning

**Status:** [x] · **P1** · **Area:** CI · merged by PR #20; all six CI jobs pass

The Ubuntu CI job currently runs normal Go tests and untagged vet, but has no `-race`, gofmt, golangci-lint, or govulncheck gate. It also does not vet the `e2e` build tag. The README notes that race detection needs a C compiler, so the race gate belongs on Ubuntu.

#### Scope

Add `go test -race ./...` on Ubuntu, a gofmt check for first-party Go source, `golangci-lint` with staticcheck/unused analyzers, `govulncheck`, and `go vet -tags e2e`. Keep the existing untagged vet check as well.

**Progress (2026-09-23):** PR #20 (`368fb18`) merged after all six CI jobs passed. The Ubuntu gates now check gofmt, `-race`, golangci-lint, govulncheck, and both normal and E2E-tagged vet. Upgraded `golang.org/x/text` to v0.39.0, the first version fixed for GO-2026-5970; regenerated the 33-package Go notice report. Removed unused code/fields, fixed staticcheck findings, formatted six first-party files, and addressed the races the detector found by snapshotting queued items under the scheduler mutex and configuring test settings before startup. Local golangci-lint, govulncheck, and tagged vet pass; the full Go test and race suites passed in GitHub CI.

### M7.3 Supply-chain automation

**Status:** [x] · **P1** · **Area:** CI / security

Before this item, `.github/` had no automated dependency or code scanning configuration.

#### Scope

- Dependabot for `gomod`, `npm`, and `github-actions`.
- CodeQL for Go and JavaScript.
- Pin actions by SHA.
- Pin Node (`.nvmrc`), Bun (`bun-version`), and the Go `toolchain` directive.
- Choose a single JavaScript package manager. CI installs with npm, tests run with Bun, and both `bun.lock` and `package-lock.json` are committed. Remove the other lockfile or add a sync check.

**Progress (2026-09-23):** Merged by [PR #22](https://github.com/ajbergh/yt-dl-go/pull/22) as `2886e37`. Added weekly Dependabot updates for Go, both npm projects, and GitHub Actions; a CodeQL workflow scans Go and JavaScript/TypeScript; every CI/release/CodeQL action reference is pinned to a full commit SHA. Node is pinned by `.nvmrc` (24.21.0), Bun by `bun-version` (1.4.2), and the Go module toolchain by `go1.26.8`. npm is the single JavaScript package manager; Bun remains the pinned test runtime and the redundant root `bun.lock` was removed. All six CI jobs and both CodeQL analyses passed.

### M7.4 Close the test gaps

**Status:** [~] · **P1** · **Area:** testing

No tests exist for:

- `muxMP4`/`muxMP4Track`;
- `convertAACToMP3` (there is no `audio_test.go`);
- `transferAdaptiveMP4`;
- the native range loop (403 refresh, retry, resume);
- `publishOutput` naming and collisions, and `sanitizePathComponent`;
- `jobBudget` concurrency;
- startup with a corrupt row;
- migrations from old DB fixtures;
- retention under `managed-only` (M0.1);
- `MAX_JOBS` with finished jobs (M0.2);
- SSE drop/overflow;
- security headers (M0.4).

#### Scope

- Add the tests above.
- Add fuzz tests for the UMP/SABR parser, WebVTT→SRT conversion, the naming tokenizer, and the URL/ID validation.
- Collect coverage reports (`go test -cover`, `bun test --coverage`) and publish them in CI summaries.

**Progress (2026-09-23):** M7.3 PR #26's first CI attempt exposed an intermittent ordering assumption in `TestCancellationQueueAndTimeout`: with a single active-item slot, playlist workers may let blocked item 2 start before item 1 completes, while the test waits for item 1 before issuing cancellation. The failed Go test passed on the workflow rerun, but the fixture still needs deterministic ordering. PR #31's race-detector run also found an actual queue ownership race: `setQueueItems` copied `j.Items` before locking while cancellation refreshed the slice under the mutex. The lock now covers the snapshot, replacement, refresh, and persistence. Both scheduler findings should be addressed before extending this milestone's coverage work.

### M7.5 Typed frontend tests and E2E robustness

**Status:** [ ] · **P2** · **Area:** testing

- Frontend tests are untyped `.mjs`: one 701-line happy-dom file that drives the whole `HomePage`. There is no `test` script.
- Browser E2E looks for Chrome only on `PATH` (`browser-e2e.mjs:12-23`), so it fails on a default Windows install.

#### Scope

- Migrate to `.test.tsx` with Testing Library, split by page, and add `npm test`.
- Probe standard Chrome and Edge install paths.
- Upload E2E service and Chrome logs when a run fails.
- Add axe checks (M4.7).

---

# Milestone 8 — Release and distribution

### M8.1 Ship the first release

**Status:** [T] Dry run validated; first release version and tag pending · **P1** · **Area:** release

- No release tag exists yet and `package.json` remains `0.1.0`; the successful dry run validated `v0.1.0` without publishing a release.
- Shared action versions now align between the release and CI workflows.
- The README's Windows download now points to GitHub Releases rather than the gitignored `dist/` directory.

#### Scope

- [x] Add a `workflow_dispatch` dry-run mode to `release.yml` that skips `gh release create` and uploads release artifacts for inspection.
- [x] Align shared action versions between the two workflows.
- Cut `v0.2.0` (or `v1.0.0` after Milestone 0).
- [x] Point the README at GitHub Releases.

**Progress (2026-09-24):** Merged by [PR #15](https://github.com/ajbergh/yt-dl-go/pull/15); all six CI jobs passed. The authorized `v0.1.0` run (35921227203) exposed a source archive checksum path issue, fixed in [PR #31](https://github.com/ajbergh/yt-dl-go/pull/31). Its rerun (35924524457) passed all seven checksum checks but exposed CRLF handling in expected-archive validation, fixed in [PR #32](https://github.com/ajbergh/yt-dl-go/pull/32). Final dry run [35925902083](https://github.com/ajbergh/yt-dl-go/actions/runs/35925902083) passed source validation, all six platform builds, archive and manifest checksums, provenance attestations, and uploaded the 77.5 MB `release-dry-run-v0.1.0` artifact. A fresh user-requested `v0.1.0` dry run [35955844770](https://github.com/ajbergh/yt-dl-go/actions/runs/35955844770) also passed on `main` (SHA `15f9369`). Source validation, browser E2E, all six platform packages, provenance attestations, source/relinking archive, checksum manifest and artifact upload passed; the 78.2 MB `release-dry-run-v0.1.0` artifact is available in the run, and the GitHub Release creation step was skipped. No `v0.1.0` tag or release was created. The first public release version/tag decision remains pending.

### M8.2 Harden the release workflow

**Status:** [x] · **P1** · **Area:** release / security

- The original findings are addressed: write/attestation permissions are scoped to the jobs that need them, expected archives come from matrix manifests, and build timestamps are normalized to UTC.
- Dry-run validation covers all archive checks, manifest generation, provenance attestation, and artifact upload.

#### Scope

- [x] Set permissions per job.
- [x] Derive the expected artifact count from per-matrix manifests.
- [x] Emit UTC timestamps on both Windows and Unix release builders.
- [x] Add Windows arm64 builds.

**Progress (2026-09-23):** Implemented in [PR #13](https://github.com/ajbergh/yt-dl-go/pull/13) as `db6f845`, with CRLF-safe expected-archive validation completed in [PR #32](https://github.com/ajbergh/yt-dl-go/pull/32). Workflow permissions are scoped by job, Windows packaging builds amd64 and arm64, build timestamps are converted to UTC, and expected archives come from per-matrix manifests. The successful [dry run 35925902083](https://github.com/ajbergh/yt-dl-go/actions/runs/35925902083) validated source, six platform builds, archive checksums, manifest generation and attestation, and uploaded the workflow artifact. All M8.2 gates are complete.

### M8.3 SBOM and reproducible archives

**Status:** [ ] · **P2** · **Area:** supply chain

- Generate and attest an SBOM (`syft` or `cyclonedx-gomod` plus the npm SBOM).
- Make the tar archives reproducible (`--sort=name --mtime=@$SOURCE_DATE_EPOCH --owner=0 --group=0`) and replace `Compress-Archive`, which embeds timestamps, with a deterministic ZIP writer.
- Verify reproducibility with two independent builds in CI.

### M8.4 Native signing and notarization

**Status:** [!] needs credentials · **P1** · **Area:** release

This item was carried forward from v1 P2.3.

#### Scope

- Windows Authenticode and macOS Developer ID/notarization in a protected GitHub environment.
- A macOS `.app` bundle or DMG.
- Signing is the prerequisite for self-update (M8.6) and for winget/Homebrew acceptance.

### M8.5 Package-manager distribution and a container image

**Status:** [ ] · **P2** · **Area:** distribution

#### Scope

- Evaluate GoReleaser to replace the two build scripts and generate Homebrew tap, Scoop, and winget manifests.
- Evaluate AUR and Flatpak later.
- Publish an optional multi-arch Docker image for `--background` mode, with Chromium included for adaptive HD and documented network-exposure requirements (`API_TOKEN`, `ALLOWED_HOSTS`).

### M8.6 Automatic self-update (carried forward)

**Status:** [!] blocked on M8.4 · **P3** · **Area:** release

The v1 P2.3 gates still apply: checksum plus signature/provenance verification, atomic replacement, keeping the known-good previous executable, restart health verification with rollback, and explicit user opt-in.

### M8.7 Changelog and commit conventions

**Status:** [ ] · **P2** · **Area:** process

- Commit styles are mixed (`feat:`, `P2.3:`, `roadmap:`, free text), and recent commits went straight to `main`.
- Adopt Conventional Commits with release-please, which bumps `package.json` and writes `CHANGELOG.md`.
- Protect `main` and require CI to pass.
- Auto-delete merged branches, and clean up the 13 stale `roadmap/*` and `fix/*` remote branches.

---

# Milestone 9 — Docs and repository hygiene

### M9.1 Community and governance docs

**Status:** [~] Security/contribution guidance and templates added; changelog pending M8.7 · **P1** · **Area:** docs

#### Scope

- [x] Add `LICENSE` (M0.6).
- [x] Add `SECURITY.md` with private vulnerability reporting guidance and the local API token/ticket model.
- [x] Add `CONTRIBUTING.md` with setup, npm/Bun roles, CI checks, and contribution guidance.
- [ ] Add `CHANGELOG.md` with M8.7 release automation.
- [x] Add bug, feature-request, and pull-request templates.

**Progress (2026-09-23):** Implemented on `roadmap/m9-1-governance-docs`; the changelog remains tied to M8.7 so it can be generated from the release history.

### M9.2 Architecture and troubleshooting docs

**Status:** [ ] · **P2** · **Area:** docs

#### Scope

- `docs/ARCHITECTURE.md`:
  - request/job lifecycle;
  - scheduler and slots;
  - progressive vs adaptive vs SABR vs browser acquisition;
  - storage policies;
  - the persistence model;
  - the SSE contract.
- `docs/TROUBLESHOOTING.md`:
  - Chrome discovery and `CHROME_PATH`;
  - Linux `zenity`;
  - why HD fell back to progressive (reading job notes);
  - proxy environments;
  - where logs and the support bundle live (M5.4).

### M9.3 Fix stale documentation

**Status:** [x] · **P1** · **Area:** docs

#### Scope

- [x] Describe the embedded UI as part of the platform executable.
- [x] Remove the sample URL's `si=` share-tracking parameter.
- [x] Mark v1's roadmap as superseded by `roadmap_v2.md`.
- [x] Verify `DATA_DIR`, `RETENTION`, and `MAX_JOBS` documentation against current behavior.

**Progress (2026-09-23):** Updated the frontend guide to describe all platform executables, removed the sample URL's share-tracking parameter, marked v1's roadmap as superseded, and confirmed the `DATA_DIR`, `RETENTION`, and `MAX_JOBS` descriptions match the current defaults and behavior.

### M9.4 Remove stray and legacy files

**Status:** [x] · **P1** · **Area:** hygiene · merged by PR #18

- [x] Delete the unreferenced, stale `SOURCE_MANIFEST.sha256`.
- [x] Delete `vite-dev-reload.ts` and remove the WebSocket mute shim and its TypeScript/lint references from the Vite setup.
- [x] Restore native Vite HMR and React Fast Refresh; remove disabled HMR and polling while preserving the watcher ignore list.
- [x] Move `tailwindcss` to `devDependencies` and refresh npm/Bun lockfiles and generated production npm notices.
- [x] Keep `dev_mock_new_ui/` available as a design reference because M4.9 has not captured its ideas yet; ESLint already excludes it and this repository has no Dependabot configuration.
- [x] Manually verify a frontend edit hot-updates through the local dev server without a full reload.

**Progress (2026-09-23):** PR #18 (`59dd1cd`) merged after all six CI jobs passed, including license drift, typecheck, lint, frontend and Go tests, browser E2E, and Linux/macOS/Windows package builds. Removed the stale manifest and SSE reload plugin, returned Vite to native HMR defaults, retained the existing file-watch exclusions, and moved Tailwind out of production dependencies. npm now reports 14 production packages in its generated notice report. The source scan found the old manifest substantially stale (62 missing paths, 40 hash mismatches, and 4 valid entries), so it was deleted rather than repaired. Manual HMR smoke check passed: editing the queue page's explanatory text updated it in the open browser, while a browser-side marker and `performance.timeOrigin` remained unchanged, confirming no full reload. The temporary edit and local servers were removed after the check.

### M9.5 Cross-platform developer workflow

**Status:** [ ] · **P2** · **Area:** DX

- `npm run dev:all` hard-codes PowerShell (`package.json:8`).
- `start-dev.sh` is not wired to any script and passes `--host` (`start-dev.sh:69`), which contradicts `vite.config.ts:141-143`.

#### Scope

- Add a `Makefile` or `Taskfile.yml` with `dev`, `build`, `test`, `lint`, `e2e`, and `package` targets.
- Add an `air` config for Go hot reload.
- Make the dev scripts bind loopback-only by default.

---

# Suggested sequencing

| Phase | Items | Rationale |
| --- | --- | --- |
| **1. Stop the bleeding** (days) | M0.1–M0.9 | Data loss, security exposure, legal status, and broken fresh clones. Each item is small and independent. |
| **2. First public release** | M8.1, M8.2, M9.1, M9.3, M9.4, M7.2, M7.3 | A licensed, attributed, linted, vuln-scanned, tagged release. |
| **3. Durable library** | M1.1–M1.5, M5.6 | Library records independent from jobs; incremental persistence; a stable data directory. |
| **4. Reliable engine** | M2.1–M2.8, M6.2, M6.3, M7.4 | Stall safety, atomic writes, resume everywhere, extractor resilience. Refactors (M6.x) land alongside the tests that protect them. |
| **5. UX and performance** | M4.1–M4.7, M5.1, M5.4 | Routes, store, progress deltas, input ergonomics, accessibility, built-in auth, diagnostics. |
| **6. Media depth** | M3.1–M3.5, M3.7, M4.9 | Container metadata, chapters, Opus passthrough, bulk operations, dedupe, mock features. |
| **7. Distribution** | M8.3–M8.5, M8.7, M7.1 | Signing, SBOM, package managers, Docker, CI matrix restructure. |
| **Later / exploratory** | M3.6, M4.11, M8.6, native tray (v1 P2.2) | Depends on signing, the durable library, and upstream stability. |

---

# Carried forward from v1

- **Native tray (v1 P2.2):** still deferred. Revisit when `gogpu/systray` menu-dispatch and macOS fixes ship and real desktop-session validation is possible. Background notifications should be designed together with M5.4 health reporting.
- **Automatic self-update (v1 P2.3):** now tracked as M8.6 and blocked on M8.4.
- **Published sidecar artwork (v1 P1.1 deferral):** now part of M3.3.

# Explicitly deferred / non-goals

Unchanged from v1:

- DRM bypass
- paywall bypass
- browser-cookie/account credential import
- private-video credential harvesting
- live HLS/DASH recording
- public multi-tenant hosting
- arbitrary non-YouTube site downloading

v2 adds:

- **Stream-protection/attestation circumvention.** When YouTube returns protection status ≥ 3 (`sabr.go:333`), the correct behavior stays "fail with a clear message," not a workaround.
- **Watched sources (M3.6) must never become unattended bulk scraping.** They are limited to explicitly configured, rights-confirmed sources with conservative polling intervals.

---

# Implementation journal

## 2026-09-23

### M8.2 release workflow hardening

**Status:** [x] Merged by PR #13; dry-run validation passed

- Scoped release write and attestation permissions to the jobs that need them.
- Added Windows amd64/arm64 matrix builds; the Windows build script cross-compiles after running native tests.
- Converted Windows and Unix build timestamps to UTC using PowerShell and Node.js, including macOS runners.
- Replaced the fixed five-archive assertion with expected archive manifests from each matrix job.
- All six PR #13 CI jobs passed. Dispatch dry run 35925902083 passed release-source validation, all six package builds, archive checksums, manifest generation and attestation, and uploaded the dry-run artifact. The tag-triggered publishing path remains pending an actual release.
- M0.6 merged in PR #14; dry run 35925902083 generated and checksummed the linked source archive. Tagged release-path validation remains pending.

### M0.6 license and notice implementation

**Status:** [T] Merged by PR #14; dispatch source-package validation passed, tag-triggered validation pending

- Added the MIT project license, generated Go and npm notices, complete dependency license texts, and LGPL decoder source/relinking instructions.
- License generation runs for Linux, Windows, and macOS target dependency sets and uses stable package links and normalized license text.
- PR #14 passed all six CI jobs, including license drift checks and production package builds for Linux amd64/arm64, macOS amd64/arm64, and Windows amd64.
- Dispatch dry run 35925902083 generated and checksummed the linked source archive and LGPL relink package. The source archive path triggered by a release tag remains untested until a release is cut.

### M8.1 first-release preparation

**Status:** [T] Dry run passed; release version and tag pending

- Added a manual workflow run that builds the release matrix, checks archive checksums, and uploads a dry-run artifact without creating a GitHub Release.
- Aligned shared GitHub Actions versions between CI and release workflows and updated the README's download link.
- PR #32 fixed CRLF handling in expected-archive verification. Successful dry run [35925902083](https://github.com/ajbergh/yt-dl-go/actions/runs/35925902083) passed source validation, all six platform builds, all archive checksums, manifest generation and attestation, and uploaded seven artifacts, including `release-dry-run-v0.1.0` (77.5 MB). The GitHub Release step was skipped and no release or tag was created. Select the actual release version and cut the tag after the remaining release prerequisites are complete.

### M9.1 community and governance docs

**Status:** [~] Guidance and templates added; changelog pending M8.7

- Added `SECURITY.md` with private vulnerability reporting guidance and the app's local API/token/ticket security boundaries.
- Added `CONTRIBUTING.md` with the npm/Bun setup distinction, developer commands, CI gates, and pull request guidance.
- Added bug-report, feature-request, and pull-request templates that steer security issues away from public disclosure and remind reporters to redact secrets.
- The `CHANGELOG.md` remains deferred to M8.7 release automation.

### Stabilization branches and 4K retest

**Status:** [x] Merged

- M0.7 is on `fix/fresh-checkout-build` (`baa8887`); M0.4 is stacked on it in `fix/security-headers-m07` (`2c61f5d`); M0.8 is stacked next in `fix/persistence-errors` (`cf34220`); M0.9 is stacked next in `fix/lint-gate` (`45f4f1d`). All four branches are pushed.
- The independent `fix/4k-browser-representation` branch (`de754d7`) now passes a network-enabled live test of `https://youtu.be/7PIji8OubXU?si=WRtj7oVFiAXW07Ra`. Browser Network response streaming captured the complete 2160p VP9 video (itag 315) and Opus audio (itag 251) without intercepting playback responses. The finalized WebM is 2,335,115,476 bytes; the job completed at height 2160. `go test ./...` and `go vet ./...` pass.
- M0.1 is implemented on `fix/safe-retention`, stacked on M0.9. The default now keeps Library records; explicit retention verifies all published media before removing managed files, and empty failed/cancelled jobs still expire after 24 hours by default. Targeted Go tests and frontend typecheck pass.
- M0.2 is implemented on `fix/active-job-cap`, stacked on M0.1. The live-job count replaces `len(s.jobs)` for admissions, the redundant fixed-size scheduler wake channel is removed, and item retry uses the same cap. Tests with 100 retained terminal records and paused/retry capacity pass, as do the full Go suite and vet.
- M0.3 is implemented on `fix/remove-preview-scaffolding`, stacked on M0.2. Production preview messaging and console forwarding are removed, and CI now scans a built JavaScript bundle for the forbidden preview strings. `npm run build:check`, typecheck, and lint pass.
- M0.5 is implemented on `fix/dev-only-origins`, stacked on M0.3. Production defaults omit the Vite origins; the `dev` build tag restores them for local development. Release and dev policy tests, the full Go suite, and vet pass.
- Both CI validation runs passed all six jobs: stacked stabilization at `8291aa0` on `roadmap/validate-stabilization`, and independent 4K capture at `de754d7` on `roadmap/validate-4k`.
- After GitHub CLI reauthentication, PR #10 consolidated and squash merged the stacked fixes as `287928e`. PR #11 updated the 4K branch onto that result, passed all six PR CI jobs, and squash merged as `677c59e`.
- M0.6 implementation is complete: MIT, linked Go and bundled npm notices, license texts, and LGPL source/relink materials are included. Dispatch dry-run source-package validation passed; tagged source-package validation remains pending a release tag.

### Roadmap v2 created

**Status:** [x] Complete

- Reviewed four areas: the download engine; the API, persistence, and security layers; the React frontend; and build/CI/release/docs.
- Verified the high-impact findings directly against the code before including them:
  - retention pruning under `managed-only`;
  - `MAX_JOBS` counting finished jobs;
  - preview-host scaffolding and console forwarding to the framing origin in the production bundle;
  - the missing `LICENSE`;
  - map-order filename expansion;
  - itag-18 audio in adaptive MP4.
- Items marked **Investigate** or **(not verified)** need reproduction before work begins.
