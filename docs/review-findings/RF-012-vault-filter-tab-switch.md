# RF-012: Vault section display resets while server filter persists

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/pages/home.tsx; src/pages/library.tsx

Evidence: Home retains libraryView across tab switches; LibraryPage unmounts and previously initialized its section/collection/source to All media defaults.

Resolution: Hydrate local navigation from the retained parent view on remount.

Validation: npm run check passes. Parent view hydration is typechecked; native tab navigation was not manually exercised.
