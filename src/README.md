# Frontend guide

The React/Vite application is embedded in and served by the Windows executable. It automatically connects to that executable's Go API to inspect YouTube links, submit jobs, follow progress, manage SQLite-backed preferences and retrieve finalized files through short-lived download tickets.

The Go server can host this interface itself, so end users normally run only `youtube-downloader.exe` and visit `http://127.0.0.1:8080`. A standalone Vite server is useful only while developing the UI.

## Develop

From the repository root:

~~~
npm ci
npm run dev
~~~

Vite listens on `http://127.0.0.1:5173`. In a second terminal, start the Go service with `go run .` from `src/server`, then open the Vite address to use the development UI; it connects to `http://127.0.0.1:8080` automatically. This address is fixed for `npm run dev`; custom Vite ports and `npm run preview` are not detected by the current connection logic. The packaged UI uses its own origin (including a custom `ADDR`), so no endpoint setup is needed in the executable.

The service's default CORS allowlist includes both `localhost:5173` and `127.0.0.1:5173`. The current UI connection logic supports the configured Vite dev port and the executable's same-origin UI; changing to another frontend origin requires adapting that logic as well as adding the exact origin to `ALLOWED_ORIGINS`. Keep `API_TOKEN` unset for the built-in UI: it does not prompt for or send bearer tokens, and protected API requests will otherwise return `401`.

## Checks and production UI build

~~~
npm run typecheck
npm run build
~~~

The UI tests are written for Bun and can be run with `bun test src/downloader.test.mjs src/ui.test.mjs`. `npm run lint` checks the whole repository, including the separate mock project under `dev_mock_new_ui`; lint currently reports unused imports and conditional-hook errors in that mock.

`npm run build` emits the static SPA to root `dist` and empties that directory first. The Go program embeds assets from `src/server/dist`, so copy the build there before compiling the executable. Do not put `DATA_DIR` under root `dist`; a build removes its contents.

~~~
New-Item -ItemType Directory -Force .\src\server\dist | Out-Null
Copy-Item -Path .\dist\* -Destination .\src\server\dist -Recurse -Force
~~~

Vite empties root `dist` on each build. If that directory also contains `youtube-downloader.exe`, run the Go build after the frontend build so the executable is recreated.

## UI/service contract

The frontend uses the fixed `http://127.0.0.1:8080` origin when running on Vite's configured development port `5173`, and the current page origin in the packaged executable. It checks the service at startup, loads jobs and preferences, retries failed startup checks every 2.5 seconds, then refreshes the job list approximately every 1.8 seconds. The UI has no endpoint or API-token prompt. With `API_TOKEN` set, its protected requests fail authentication; unset it for the bundled UI. It calls:

- `GET /api/health` to confirm a native Go service is ready.
- `POST /api/inspect` to retrieve metadata and supported qualities before queuing.
- `GET /api/settings` and `PUT /api/settings` to load and save the default quality in SQLite.
- `POST /api/jobs` to create a video or playlist job.
- `GET /api/jobs` to load and poll job state; `GET /api/jobs/{id}` is part of the API contract but is not needed by the current page.
- `POST /api/jobs/{id}/pause`, `/resume`, `/cancel`, and `/retry`, plus `DELETE /api/jobs/{id}`, for job management.
- `POST /api/jobs/{id}/ticket` to obtain a five-minute link for a ZIP or individual file.
- `GET /api/downloads/{ticket}` to stream the ticketed file or ZIP. Ticket URLs are capabilities and do not contain the API bearer token.

The quality choices are maximum heights. A completed file reports its actual `height` and `mimeType`; the job `note` discloses adaptive-HD or progressive-fallback behavior. The full request and response contract is in [server/README.md](server/README.md).

## Source map

| Location | Responsibility |
| --- | --- |
| `pages/home.tsx` | Downloader workflow and visible job state |
| `lib/downloader.ts` | YouTube URL parsing, API types/client, and formatting helpers |
| `components/` | Reusable interface components and states |
| `downloader.test.mjs`, `ui.test.mjs` | Bun tests for URL/API helpers and the React page/API integration |
| `index.css` | Theme and Tailwind styles |

For executable packaging and runtime requirements, see the repository [README](../README.md).
