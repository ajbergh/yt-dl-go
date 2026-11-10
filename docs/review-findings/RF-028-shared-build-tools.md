# RF-028: Desktop build executes tools from a predictable shared temp directory

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: scripts/build-desktop.mjs

Evidence: The build uses os.tmpdir()/yt-dl-go-desktop-tools-v2.17.1 and skips installation if an executable already exists there.

Resolution: Desktop builds use a newly created unique tools directory for each process, never trusting existing executables in a predictable shared cache. An exit handler removes only the verified generated directory, including failures before package staging.

Validation: node --check scripts/build-desktop.mjs; existing desktop packaging suite passes. Full native installer builds were not rerun.
