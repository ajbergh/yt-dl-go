# Frontend guide

The React/Vite interface is embedded in both the Wails desktop app and the
browser/server executable. Users normally open the desktop app in its native
window. Both editions share the HTTP API for inspection, queue/Library state,
preferences, media streaming, and events.

In desktop mode a Wails binding supplies a private ephemeral API origin and
per-launch bearer. The UI authenticates its requests and uses Wails bindings for
native saves and folder dialogs. In browser/server mode it uses the page origin;
Vite development uses the configured loopback API below. See the
[desktop guide](../docs/DESKTOP.md) for native build/runtime details.

## Develop

From the repository root:

~~~
npm ci
npm run dev
~~~

Vite listens on `http://127.0.0.1:5173`. In a second terminal, start the Go service with `go run -tags=dev .` from `src/server`, then open the Vite address to use the development UI; it connects to `http://127.0.0.1:8080` automatically. This address is fixed for `npm run dev`; custom Vite ports and `npm run preview` are not detected by the current connection logic. The browser/server packaged UI uses its own origin (including a custom `ADDR`), so no endpoint setup is needed in the executable.

The `dev` build tag adds `localhost:5173` and `127.0.0.1:5173` to the default CORS allowlist. Release builds default to their own listener origin only. The current UI connection logic supports the configured Vite dev port and the executable's same-origin UI; changing to another frontend origin requires adapting that logic as well as adding the exact origin to `ALLOWED_ORIGINS`. Keep `API_TOKEN` unset for the browser/server development UI, which has no token-entry flow. Desktop authentication comes from the Wails bootstrap binding instead.

## Checks and production UI build

Settings opens with download location, folder organization, notifications, and a summary of the saved video defaults. The Advanced settings toggle reveals format and quality overrides, transfer limits, storage policy, filename templates, permissions, runtime settings, and diagnostics. Collapsing it preserves edits; Save preferences also saves collapsed fields. Use automatic video defaults restores best quality, automatic stream selection for each video, and disabled 360p recovery without changing other preferences.

~~~
npm run typecheck
npm run build
~~~

The UI tests are written for Bun and can be run with `bun test src/downloader.test.mjs src/ui.test.mjs`. CI also runs `npm run e2e:browser`, which requires Chrome/Chromium and Go, builds the service with the test-only `e2e` build tag, and exercises the real browser/service/SQLite workflow. The fixture build is never used by the production executable. CI runs `npm run lint` against the production project; the separate `dev_mock_new_ui` prototype is excluded from that gate.

Build directly into the embed directory when preparing Go builds:

```sh
npm run build -- --outDir src/server/dist
```

A bare `npm run build` emits to root `dist` and clears that directory first.
Never store application data or hand-maintained assets there. The shared desktop
builder handles embedding automatically:

```sh
npm run build:desktop
npm run test:desktop
```

Native checks require platform prerequisites. Use `desktop,webkit2_41` Go tags
and Xvfb on Linux as documented in [DESKTOP.md](../docs/DESKTOP.md).

## UI/service contract

Connection selection is: Wails bootstrap origin/bearer for desktop, fixed
`http://127.0.0.1:8080` on Vite's configured port `5173`, or page origin in the
browser/server package. Startup loads jobs and preferences, retries failed checks
every 2.5 seconds, and consumes authenticated SSE with a 30-second reconciliation
fallback. There is no endpoint/token prompt. Desktop uses the native bridge for
user-confirmed saves/folder selection while streaming previews and events over
HTTP. It calls:

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
| `pages/settings.tsx` | Preferences, download location, advanced settings, and application diagnostics |
| `hooks/use-service.ts` | Backend bootstrap, SQLite hydration, SSE updates, reconciliation, readiness/errors, and terminal notifications |
| `hooks/use-jobs.ts` | Job mutations, retries, tickets, filesystem actions, queue/playlist ordering, batch actions, and cleared-queue persistence |
| `hooks/use-settings.ts` | Settings save/mutation behavior, folder selection, notification permission, and category management |
| `components/downloader/view-model.tsx` | Downloader view types, labels, formatting/selection helpers, thumbnail loading, and sortable queue-row UI |
| `lib/downloader.ts` | YouTube URL parsing, API types/client, service-event streaming, and formatting helpers |
| `downloader.test.mjs`, `ui.test.mjs` | Bun tests for URL/API helpers and the React page/API integration |
| `index.css` | Theme and Tailwind styles |

For executable packaging and runtime requirements, see the repository [README](../README.md).

## Documentation screenshots

README images live in `docs/assets/screenshots/`. Capture the actual Wails window
with disposable data. Playwright can capture embedded WebView2 content; identify
that capture method when the native OS frame is omitted. Do not substitute the
`dev_mock_new_ui` prototype or browser E2E screenshots for native-app evidence.
Avoid personal paths, account information, private media, bearer tokens, or ticket
URLs. Browser E2E screenshots remain temporary test artifacts unless deliberately
reviewed and added as documentation assets.
