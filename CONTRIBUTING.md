# Contributing

Thanks for helping improve yt-dl-go. Keep changes focused, explain user-visible behavior, and include the checks relevant to your change in the pull request.

## Development setup

The project uses npm for frontend dependency installation and builds. Bun is used to run the frontend test files; it does not replace npm. Install Go 1.26 or newer, Node.js 24 with npm, Bun, and Git. Browser end-to-end checks also need Chrome or Chromium.

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

The frontend at `http://127.0.0.1:5173` connects to the Go service at `http://127.0.0.1:8080`. Leave `API_TOKEN` unset when using the bundled UI; it does not send bearer tokens.

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

If you change dependencies or license notices, update the corresponding lockfile and generated notice files. To regenerate notices, install the pinned scanner tools as CI does, then run `npm run licenses:generate`.

## Pull requests and commits

Use a short subject that describes the change, such as `docs: clarify release downloads` or `Fix retry queue ordering`. Conventional Commit prefixes are welcome; release automation and enforced commit conventions are tracked separately in the roadmap.

In the pull request, summarize the behavior change, list the checks you ran and their results, and note any documentation, security, or licensing impact. Include screenshots for meaningful UI changes. Do not attach credentials, ticket URLs, signed media URLs, or private media to public issues or pull requests; see [SECURITY.md](SECURITY.md) for vulnerability reports.
