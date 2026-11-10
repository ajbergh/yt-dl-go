# RF-017: Unix dev script omits backend dev build tag

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: scripts/start-dev.sh

Evidence: The backend used go run . while dev UI origins require the dev build.

Resolution: Start the backend with go run -tags dev .

Validation: Bash syntax validation passes in Debian WSL. The backend invocation includes the dev tag; the complete interactive dev environment was not started.
