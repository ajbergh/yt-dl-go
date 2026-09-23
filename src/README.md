# Frontend guide

The React/Vite application is embedded in and served by the Windows executable. It automatically connects to that executable's Go API to inspect YouTube links, submit jobs, follow progress, manage SQLite-backed preferences and retrieve finalized files through short-lived download tickets.

The Go server can host this interface itself, so end users normally run only `youtube-downloader.exe` and visit `http://127.0.0.1:8080`. A standalone Vite server is useful only while developing the UI.

## Develop

From the repository root:

~~~
npm ci
npm run dev
~~~

Vite listens on `http://127.0.0.1:5173`. In a second terminal, start the Go service with `go run -tags=dev .` from `src/server`, then open the Vite address to use the development UI; it connects to `http://127.0.0.1:8080` automatically. This address is fixed for `npm run dev`; custom Vite ports and `npm run preview` are not detected by the current connection logic. The packaged UI uses its own origin (including a custom `ADDR`), so no endpoint setup is needed in the executable.

The `dev` build tag adds `localhost:5173` and `127.0.0.1:5173` to the default CORS allowlist. Release builds default to their own listener origin only. The current UI connection logic supports the configured Vite dev port and the executable's same-origin UI; changing to another frontend origin requires adapting that logic as well as adding the exact origin to `ALLOWED_ORIGINS`. Keep `API_TOKEN` unset for the built-in UI: it does not prompt for or send bearer tokens, and protected API requests will otherwise return `401`.

## Checks and production UI build

~~~
npm run typecheck
npm run build
~~~

The UI tests are written for Bun and can be run with `bun test src/downloader.test.mjs src/ui.test.mjs`. CI also runs `npm run e2e:browser`, which requires Chrome/Chromium and Go, builds the service with the test-only `e2e` build tag, and exercises the real browser/service/SQLite workflow. The fixture build is never used by the production executable. CI runs `npm run lint` against the production project; the separate `dev_mock_new_ui` prototype is excluded from that gate.

`npm run build` emits the static SPA to root `dist` and empties that directory first. The Go program embeds assets from `src/server/dist`, so copy the build there before compiling the executable. Do not put `DATA_DIR` under root `dist`; a build removes its contents.

~~~
New-Item -ItemType Directory -Force .\src\server\dist | Out-Null
Copy-Item -Path .\dist\* -Destination .\src\server\dist -Recurse -Force
~~~

Vite empties root `dist` on each build. If that directory also contains `youtube-downloader.exe`, run the Go build after the frontend build so the executable is recreated.

## UI/service contract

The frontend uses the fixed `http://127.0.0.1:8080` origin when running on Vite's configured development port `5173`, and the current page origin in the packaged executable. It checks the service at startup, loads jobs and preferences, retries failed startup checks every 2.5 seconds, then consumes authenticated Server-Sent Events for live job/settings updates with a 30-second full-job reconciliation as a safety net. The UI has no endpoint or API-token prompt. With `API_TOKEN` set, its protected requests fail authentication; unset it for the bundled UI. It calls:

- `GET /api/health` to confirm a native Go service is ready.
- `POST /api/inspect` to retrieve metadata and supported qualities before queuing.
- `GET /api/settings` and `PUT /api/settings` to load and save the default quality in SQLite.
- `POST /api/jobs` to create a video or playlist job.
- `GET /api/jobs` to load and periodically reconcile job state; `GET /api/events` supplies the normal live job/settings stream. `GET /api/jobs/{id}` remains part of the API contract but is not needed by the current UI.
- `POST /api/jobs/{id}/pause`, `/resume`, `/cancel`, and `/retry`, plus `DELETE /api/jobs/{id}`, for job management.
- `POST /api/jobs/{id}/ticket` to obtain a five-minute link for a ZIP or individual file.
- `GET /api/downloads/{ticket}` to stream the ticketed file or ZIP. Ticket URLs are capabilities and do not contain the API bearer token.

The quality choices are maximum heights. A completed file reports its actual `height` and `mimeType`; the job `note` discloses adaptive-HD or progressive-fallback behavior. The full request and response contract is in [server/README.md](server/README.md).

## Source map

| Location | Responsibility |
| --- | --- |
| `pages/home.tsx` | Top-level orchestration, derived queue/library state, inspection submission, navigation, and page composition |
| `pages/queue.tsx` | Add/inspect workflow, batch controls, queue filters, queue cards, and drag/drop presentation |
| `pages/library.tsx` | Library filtering/layout, file cards, preview/save/filesystem controls, and scoped-delete presentation |
| `pages/settings.tsx` | Service status plus preference/output configuration UI |
| `hooks/use-service.ts` | Backend bootstrap, SQLite hydration, SSE updates, reconciliation, readiness/errors, and terminal notifications |
| `hooks/use-jobs.ts` | Job mutations, retries, tickets, filesystem actions, queue/playlist ordering, batch actions, and cleared-queue persistence |
| `hooks/use-settings.ts` | Settings save/mutation behavior, folder selection, notification permission, and category management |
| `components/downloader/view-model.tsx` | Downloader view types, labels, formatting/selection helpers, thumbnail loading, and sortable queue-row UI |
| `lib/downloader.ts` | YouTube URL parsing, API types/client, service-event streaming, and formatting helpers |
| `downloader.test.mjs`, `ui.test.mjs` | Bun tests for URL/API helpers and the React page/API integration |
| `index.css` | Theme and Tailwind styles |

For executable packaging and runtime requirements, see the repository [README](../README.md).
