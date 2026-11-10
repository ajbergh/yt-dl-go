# RF-018: Every progress update rerenders the Queue row list

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/hooks/use-service.ts; src/pages/queue.tsx

Evidence: Each progress event replaces jobs state and the parent renders inline rows for the full filtered queue without memoization or virtualization.

Resolution: Extracted memoized Queue rows use per-row render signatures excluding aggregate job progress. Layout-updated stable commands retain current callbacks across skipped renders. Individual progress updates leave unrelated jobs and playlist siblings unrendered.

Validation: npm run test:review instruments actual TSX row renders and verifies unchanged jobs/siblings are skipped and skipped rows invoke current callbacks.
