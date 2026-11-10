# RF-029: Playlist-wide controls only appear on the first item row

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/pages/queue.tsx

Evidence: The row template guards job selection and playlist-wide controls with item.index === 1. Filtering out that row hides access to those controls.

Resolution: Playlist-wide controls, selection, editor and history actions use the first visible item for each job, so filtering out item 1 does not hide them.

Validation: Frontend regression renders a playlist with only item 2 visible and checks playlist controls and selection.
