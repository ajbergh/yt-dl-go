# Desktop release checklist

Use this checklist for each candidate; substitute its version, source commit, and
run IDs. The historical browser/server v0.1.0 publication is summarized in
[RELEASES.md](RELEASES.md#published-release-history). Do not reuse or move that tag
for the desktop release. Select a new version and update `package.json` before tagging.

## Current Wails candidate evidence

Recorded 2026-11-05 for PR #135, build source `7563843`:

| Check | Evidence/status |
| --- | --- |
| Source validation and production packages | [CI passed](https://github.com/ajbergh/yt-dl-go/actions/runs/42044326071) |
| CodeQL | [Passed](https://github.com/ajbergh/yt-dl-go/actions/runs/42044326081) |
| Six native desktop targets | [Passed](https://github.com/ajbergh/yt-dl-go/actions/runs/42044326094) |
| Full release dry run and final assembly | [Passed](https://github.com/ajbergh/yt-dl-go/actions/runs/42044315998) |
| Windows manual verification | Passed, reported by the maintainer on 2026-11-05 |
| macOS and Linux manual verification | Pending |
| Credentialed macOS app/PKG signing and clean-Mac acceptance | Pending |
| Windows Authenticode | Pending |

The manual Windows report applies to the tested Windows build; it is not a claim
that every architecture or later changed build has been manually exercised.
Documentation-only changes preserve this build evidence. Re-run relevant checks
when application, dependency, build, signing, or packaging behavior changes.

## 1. Select and validate the source

- [ ] Select the intended source commit, review changes, and confirm current CI/CodeQL.
- [ ] Choose a new stable version matching `package.json` exactly (`vMAJOR.MINOR.PATCH`).
- [ ] Confirm notices, licenses, LGPL source/relinking, and changelog are current.
- [ ] Dispatch Release against the candidate ref with that version. Dispatch and
      `release-validation/v*.*.*` branches must not create a release or tag.

## 2. Verify all release files

- [ ] Confirm portable and installer artifacts for Windows, macOS, and Linux,
      each in AMD64 and ARM64, using the [artifact matrix](PACKAGING.md).
- [ ] Confirm all six browser/server archives and the corresponding source archive.
- [ ] Confirm native CI installed, reinstalled, launched, and removed each installer
      and preserved application data.
- [ ] Download the assembled dry-run artifact and check every final checksum:

```sh
gh run download RUN_ID --repo ajbergh/yt-dl-go \
  --name release-dry-run-VERSION --dir release-validation
cd release-validation
sha256sum --check SHA256SUMS.txt
# macOS alternative: shasum -a 256 -c SHA256SUMS.txt
```

- [ ] Verify provenance for final files and the manifest, for example:

```sh
gh attestation verify youtube-vault-windows-amd64-setup.exe --repo ajbergh/yt-dl-go
gh attestation verify SHA256SUMS.txt --repo ajbergh/yt-dl-go
```

- [ ] Verify `--version`, Settings, source commit, and UTC build date agree.
      Checksums and provenance do not establish native platform signing.

## 3. Manual native acceptance

Use isolated data and permitted media. Record tested artifact hashes and hardware;
cover each OS family and the architectures intended for publication.

- [ ] Install and open through the normal OS entry point; also test the portable package.
- [ ] Inspect a permitted video/playlist; exercise queue events, pause/resume, and retry.
- [ ] Complete a permitted download and verify actual quality and file playback.
- [ ] Verify Library previews/seeking, native save/cancel, ZIP export, and folder selection.
- [ ] Close/restart and verify queue, Library, preferences, and output locations.
- [ ] Verify installer reinstallation/removal retains saved data and published files.
- [ ] Record dependencies, permissions prompts, quarantine, and signing warnings.

## 4. Signing and publication

- [ ] Configure protected signing credentials if signed distribution is intended.
      Verify Windows executable/setup signatures and macOS app/PKG Developer ID,
      notarization, stapling, and clean-machine acceptance.
- [ ] Hash and attest final bytes after signing; do not reuse pre-signing hashes.
- [ ] Preserve a trusted prior build and a compatible complete data backup for rollback.
- [ ] Push the approved new stable tag only after source/version review. The tag
      workflow creates a draft, not a published release.
- [ ] Review final assets, checksums, provenance, notes, manual acceptance, and signing
      status. Explicitly disclose unsigned packages and any accepted limitations.
- [ ] Publish only after maintainer sign-off. Keep automatic self-update disabled.

A successful dry run validates the pipeline; it does not publish a desktop release.
