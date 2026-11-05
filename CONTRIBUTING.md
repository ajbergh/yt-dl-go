# Contributing

Thanks for helping improve yt-dl-go. Keep changes focused, explain user-visible behavior, and include the checks relevant to your change in the pull request.

## Development setup

The project uses npm 11.19.0 for frontend dependency installation and builds. Bun 1.4.2 runs the frontend test files; it does not install packages or replace npm. Install the pinned Node.js version from `.nvmrc`, Go 1.27.3 or newer (as specified by `src/server/go.mod`), Bun 1.4.2, and Git. Browser end-to-end checks also need Chrome or Chromium.

TypeScript and the ESLint parser must be upgraded together within the parser's supported peer range. The current `typescript-eslint` release supports TypeScript below 6.1; this project uses TypeScript 6.0.3. Dependabot defers TypeScript 7 until parser support is available. React and ReactDOM updates are grouped so their runtime versions stay aligned.

From the repository root, install dependencies and start the frontend:

~~~sh
npm ci
npm run dev
~~~

In a second terminal, start the local Go service:

~~~sh
cd src/server
go run -tags=dev .
~~~

This browser development workflow connects `http://127.0.0.1:5173` to `http://127.0.0.1:8080`; leave `API_TOKEN` unset for it. Native desktop builds use Wails bootstrap authentication instead.

## Desktop builds

For native work, install the platform dependencies in [DESKTOP.md](docs/DESKTOP.md),
then run `npm run build:desktop` and `npm run test:desktop`. Build on the target OS;
macOS/Linux need matching architecture. Windows packaging requires NSIS 3.11.
Use `node --test scripts/desktop-packaging.test.mjs` for packaging regressions.
The six-target Desktop builds workflow tests final portable files and installer
integration. Manual platform acceptance is tracked in the [release checklist](docs/RELEASE_CHECKLIST.md).

The icon source is `public/app-icon.svg`. Run `npm run icons:generate` after edits;
commit all generated assets and run `npm run icons:check`.

## Checks

Run the checks that cover your change. The CI workflow is the authoritative list of required gates:

~~~sh
npm ci --prefix tools/license-checker
go install github.com/google/go-licenses/v2@v2.0.1
npm run licenses:check
npm run typecheck
npm run lint
npm run build:check
npm run build -- --outDir src/server/dist
bun test src/downloader.test.mjs src/ui.test.mjs
cd src/server
go test ./...
go test -tags=dev -run TestDevelopmentOriginsIncludeVite ./...
go vet ./...
cd ../..
npm run e2e:browser
~~~

The browser E2E checks require Go and Chrome or Chromium. The embedded frontend build writes into `src/server/dist`; do not put `DATA_DIR` under the root `dist` directory because frontend builds clear that directory.

Frontend bundles and Go build caches are generated and ignored. Only the embedded `dist/.gitkeep` placeholder is tracked; Vite copies it from `public/` so builds keep the worktree clean. Build the embedded frontend before running the Go service. To inspect browser-test screenshots, set `E2E_SCREENSHOT_DIR` to a temporary output directory before running `npm run e2e:browser`.

If you change dependencies or license notices, update the corresponding lockfile and generated notice files. To regenerate notices, install the pinned scanner tools as CI does, then run `npm run licenses:generate`.

## Pull requests and commits

Use a short subject that describes the change, such as `docs: clarify release downloads` or `Fix retry queue ordering`. Conventional Commit prefixes are welcome; release automation and enforced commit conventions are tracked in the [roadmap](roadmap.md).

In the pull request, summarize the behavior change, list the checks you ran and their results, and note any documentation, security, or licensing impact. Include screenshots for meaningful UI changes. Do not attach credentials, ticket URLs, signed media URLs, or private media to public issues or pull requests; see [SECURITY.md](SECURITY.md) for vulnerability reports.

## Documentation

Keep the root README focused on installation and everyday use. Put build/runtime
instructions in `docs/DESKTOP.md`, artifact naming in `docs/PACKAGING.md`, publication
gates in the release policy/checklist, and implementation details in the frontend
and engine guides. Consolidate useful investigation conclusions into those guides;
Git history retains dated experiment logs. Preserve legal notices, dependency
license/source files, and fixture/upstream provenance.

For desktop screenshots, use a disposable library and commit reviewed images under
`docs/assets/screenshots/`; identify the platform and whether the image captures the embedded WebView or the
whole native window.
Do not publish private library content, personal paths, credentials, or ticket URLs.
Check relative links and images after moving or deleting a document.
