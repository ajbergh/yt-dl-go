# RF-030: Live reconnection overwrites unsaved settings edits

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/hooks/use-service.ts: snapshot/settings-changed; src/hooks/use-settings.ts

Evidence: The service hook writes live settings into the same settings state used as an editable draft; dirty tracking in useSettings does not guard these writes.

Resolution: Service hydration retains an authoritative settings baseline and merges incoming values only into fields the user has not edited. Locally changed fields and arrays survive snapshots/reconnection while unrelated server changes remain visible.

Validation: Frontend regression checks dirty scalar/array preservation and unrelated authoritative updates; npm run check passes.
