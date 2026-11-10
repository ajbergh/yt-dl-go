# RF-002: Unix publication fails on filesystems without hard links

Severity: high

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/durable_unix.go

Evidence: durableLink returned every os.Link error without an alternative publication path. No exFAT/network mount was tested.

Resolution: Unsupported hard-link errors use an exclusive synced copy; source removal follows durable destination publication. Existing destinations are preserved.

Validation: TestDurableLinkUnsupportedFilesystem and TestPublishTemporaryDoesNotReplaceExistingFile passed in Debian WSL. Linux amd64/macOS arm64 test binaries cross-compile. Physical exFAT/network mounts and macOS runtime were not exercised.
