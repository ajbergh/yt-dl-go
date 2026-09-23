# Security policy

## Reporting a vulnerability

Please do not disclose a suspected vulnerability in a public issue or pull request. Use GitHub's private vulnerability reporting feature for this repository if it is enabled. If it is unavailable, open an issue that asks the maintainers for a private reporting channel and does not include exploit details.

Include the affected version or commit, operating system, the relevant configuration, steps to reproduce, and the impact. A small proof of concept is helpful when it is safe to share privately. Do not include `API_TOKEN` values, download-ticket URLs, signed media URLs, cookies, private media, or other credentials in a report or log attachment. There is no guaranteed response time; maintainers will coordinate disclosure after understanding and addressing the issue.

## Application security boundaries

yt-dl-go is designed as a trusted, single-user local application. By default it listens on `127.0.0.1:8080`. Loopback access is intentionally unauthenticated so the bundled UI can use the API. Do not expose this default setup to a network you do not trust.

For a non-loopback listener, configure an `API_TOKEN` of at least 32 characters and exact `ALLOWED_HOSTS` and `ALLOWED_ORIGINS` values. Put network-visible access behind TLS and appropriate network controls. Host and origin allowlists do not replace authentication or network protection. The bearer token protects the API routes used to inspect and manage jobs; `/api/health` is public, and `/api/downloads/<ticket>` is authorized by its ticket instead of the bearer token. The bundled UI does not send bearer tokens; setting `API_TOKEN` while using that UI makes protected API requests fail. Use a separate client that can send the token.

Completed media is downloaded through short-lived ticket URLs. Archive and file tickets expire after five minutes; inline preview tickets expire after 30 minutes. A ticket URL grants access to its file or archive until it expires or is invalidated with its job, so treat it like a temporary bearer credential and do not share or publish it. API bearer tokens and ticket URLs should be removed from logs and issue reports.

The project does not support importing browser cookies or account credentials, bypassing DRM or paywalls, or circumventing YouTube stream protection. Report security issues in the application itself; do not test against systems or accounts without authorization.
