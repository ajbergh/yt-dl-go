# First public release verification — v0.1.0

**Historical runbook:** [v0.1.0 was published on October 10, 2026](https://github.com/ajbergh/yt-dl-go/releases/tag/v0.1.0) with explicit approval and disclosed outstanding signing, independent attestation verification, and manual end-to-end testing. Unchecked tasks below are **not** represented as completed; reuse this checklist for future releases, updating the version to match `package.json`.

## 1. Check the source revision

- [ ] Select an exact `main` commit with green CI and CodeQL checks, no outstanding required review, and no known release-blocking issue.
- [ ] Verify `package.json` matches the intended tag exactly (`v<version>`).
- [ ] Review changes since the previous dry run and ensure third-party notices and LGPL relinking material are current.
- [ ] Confirm that the release is limited to content users own or are permitted to download, as documented in the README.

## 2. Exercise the nonpublishing release pipeline

Either dispatch **Release** with `version=v0.1.0` against the chosen source ref, or push a branch named `release-validation/v0.1.0` containing the workflow and desired candidate commit. A validation-branch push must **not** create a GitHub Release or tag. Only a stable-version **tag push** may prepare a draft GitHub Release.

After the validation run succeeds, download its assembled artifact:

```bash
gh run download RUN_ID --repo ajbergh/yt-dl-go \
  --name release-dry-run-v0.1.0 --dir release-validation
cd release-validation
sha256sum --check SHA256SUMS.txt
```

Use `shasum -a 256 -c SHA256SUMS.txt` instead on a macOS host without `sha256sum`. Check that the manifest includes all **seven** archives: Windows amd64 and arm64, Linux amd64 and arm64, macOS amd64 and arm64, and the LGPL-compatible source archive. All archived checksums must pass. Confirm the workflow uploaded the manifest and reported successful provenance attestations.

## 3. Verify provenance and release identities

- [ ] Verify the source commit used in the run matches the candidate, and ensure all six platform packages were built from it.
- [ ] For each final archive and the checksum manifest, verify GitHub artifact provenance against this repository, for example:

```bash
gh attestation verify youtube-downloader-linux-amd64.tar.gz --repo ajbergh/yt-dl-go
gh attestation verify SHA256SUMS.txt --repo ajbergh/yt-dl-go
```

- [ ] Confirm native executable `--version` prints the expected version, source commit and UTC build date. The release workflow also extracts native packages and smoke-tests API readiness, embedded UI, private SQLite storage and restart before attestation. Cross-built architectures still require a compatible real machine or emulator; Windows graceful console shutdown remains a manual gate.
- [ ] Do not confuse a GitHub build-provenance attestation or SHA-256 hash with Authenticode signing or Apple Developer ID notarization.

## 4. Smoke-test actual end-user packages

Check at least one **native** instance for each supported OS family, plus arm64 targets on arm64 hardware when available. These steps should use isolated disposable user data, not a maintainer's real library.

- [ ] Extract the archive on Windows, Linux, and macOS and verify executable startup, `--version`, and opening the embedded UI.
- [ ] Verify a healthy loopback `GET /api/health` endpoint and the Settings version/build metadata.
- [ ] Verify Library navigation, a permitted sample download, finalized file access, and orderly shutdown/restart recovery.
- [ ] Confirm `DATA_DIR` lives under the per-user application-data location and published downloads default to `~/Downloads/YouTube_Vault` (platform-appropriate home directory).
- [ ] On a sample authorized video with compatible delivery, verify progressive playback. Test browser-assisted adaptive HD separately on a supported Chromium-based browser, without promising any particular resolution.
- [ ] Smoke-test at least one non-destructive ZIP/file ticket and check no ticket secrets or signed media URLs leak into logs.
- [ ] Record any platform-specific permissions prompts, missing dependencies, quarantines, and installation warnings.

## 5. Signature, publishing, and rollback gates

- [ ] Decide whether the first public package may explicitly remain unsigned. Do **not** present it as natively signed or notarized unless the protected signing stages and real-platform verification have succeeded.
- [ ] If signing changes any executable bytes, rebuild/repackage/recompute hashes and reattest the **final** distributable bytes.
- [ ] Preserve a trusted previous build and a compatible SQLite backup before migration/rollback tests. Downgrading a newer schema into an old executable is not guaranteed.
- [ ] Only after checks are complete, push the stable `v0.1.0` **tag** at the approved `main` commit; the tag workflow creates a **draft**, not a published release.
- [ ] Review the generated draft's files, release notes, manifest, provenance, and signing status.
- [ ] Publish only after a human signs off on final assets and limitations. Draft releases remain invisible to the app's update discovery.
- [ ] Keep automatic self-update disabled until native signing, atomic replacement, restart verification, and rollback are implemented.

## Exit criteria

A successful validation workflow by itself is **not** a released product. A first public release is complete only when the final tag's draft pipeline and artifact verification are green, native package limitations are disclosed, and the draft is deliberately published.
