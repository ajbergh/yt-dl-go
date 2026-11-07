# Release and update policy

The project separates **release discovery**, **build provenance**, **native platform signing**, and **installation**. They are different trust boundaries and must not be collapsed into one “auto-update” switch.

## Desktop releases

The Wails branch produces Windows, macOS, and Linux desktop apps, each for AMD64
and ARM64, with both portable packages and installers. See [PACKAGING.md](PACKAGING.md)
and [DESKTOP.md](DESKTOP.md). The browser/server edition remains available separately.

[v0.2.0](https://github.com/ajbergh/yt-dl-go/releases/tag/v0.2.0) was published
on 2026-11-05. Its earlier full dry run on `7563843` passed all six native
targets, all six browser/server packages, installer integration, data preservation,
final coverage, checksums, and provenance generation. Windows manual acceptance
passed, reported by the maintainer on 2026-11-05. macOS/Linux manual acceptance
and credentialed signing verification remain pending. These historical results
do not replace verification of the next release's final artifacts; see
[RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md).

Version 0.3.0 makes the selected download folder the sole Library media location
and adds native menus/confirmations. Its maintainer confirmed download and dialog
testing on 2026-11-06. Release artifacts still require the workflow, checksum,
provenance, and platform disclosures below before publication.

## Published release history

[v0.1.0](https://github.com/ajbergh/yt-dl-go/releases/tag/v0.1.0) was published on
2026-10-10 from `e285dbec5ca3159992cbf41d905b2d28f9b9553f`. It contains the earlier
browser/server edition: six platform archives and a source archive, with hashes
and a manifest. Windows had no Authenticode signature; macOS had no Developer ID
signature or notarization. Independent GitHub attestation verification returned
HTTP 404 at publication despite successful workflow attestation steps. These are
historical limitations, not a description of the newer desktop dry run.

## Build identity

The application reports build metadata through `GET /api/health`, Settings, and the packaged executable:

```text
youtube-vault --version
# Browser/server edition: youtube-downloader --version
```

The reported identity includes:

- semantic version
- source commit
- UTC build timestamp

Direct `go build` defaults to `dev` / `unknown`. Desktop packaging defaults to `dev` but records the source commit and build time. Release jobs supply the same version, source commit, and UTC commit timestamp to every package.

A release build may query GitHub's public latest-release endpoint. A newer **published** release is shown as an informational link in Settings. Draft releases are invisible to that endpoint. The application does **not** download, replace, execute, or install release assets.

## Version source of truth

`package.json` is the release-version source of truth.

A release tag must be exactly:

```text
v<package.json version>
```

Example: package version `0.3.0` requires tag `v0.3.0`.

The release workflow rejects malformed tags and fails before tests or packaging if the stable tag does not match `package.json`.

## Tag release workflow

Pushing a matching stable semantic-version tag runs `.github/workflows/release.yml`.

The workflow:

1. checks out the immutable tag;
2. validates the stable tag format and exact `package.json` version match;
3. runs frontend type-check/build, Bun tests, Go tests/vet, and real-browser E2E;
4. builds both editions for Windows, Linux, and macOS on AMD64/ARM64, including desktop portable packages and installers;
5. embeds the same version, source commit, and build timestamp into every executable;
6. creates a tagged source archive with the LGPL decoder source and relinking guide;
7. requires all expected portable/installer artifacts, verifies final hashes, and creates `SHA256SUMS.txt`;
8. creates GitHub/Sigstore build-provenance attestations for every release archive and the checksum manifest;
9. creates a **draft** GitHub Release containing the binaries, source archive, and checksums.

The workflow intentionally stops at a draft. This prevents the application's latest-release check from advertising a tag-driven package before a human has completed the publication gate.

## Provenance verification

The release workflow uses GitHub artifact attestations backed by OIDC/Sigstore. This establishes which repository, workflow, commit, and build produced an archive.

For example:

```bash
gh attestation verify youtube-vault-windows-amd64-setup.exe --repo ajbergh/yt-dl-go
```

SHA-256 verification remains independent and should also be performed.

Build provenance is **not** a substitute for Windows Authenticode, Apple Developer ID signing, or Apple notarization.

## Native platform signing

Do not present a stable Windows/macOS release as natively signed until these platform trust steps are implemented with protected credentials.

### Windows

The standalone executable, packaged application executable, and setup EXE should be Authenticode-signed using a protected code-signing identity. Verify each final signature after signing. Because signing changes executable bytes, package checksums and provenance for the final artifact must correspond to the signed binary.

### macOS

A public macOS package should use Apple Developer ID signing and the appropriate notarization/stapling flow for its final distribution shape. The protected Wails path signs the `.app` with Developer ID Application, notarizes/staples it, and packages it. The final PKG uses a separate Developer ID Installer identity and is notarized/stapled before hashing and attestation. See [protected signing setup](DESKTOP.md#protected-macos-signing). Ordinary validation builds use an ad-hoc app signature for execution, not Developer ID signing or notarization.

### Linux

There is no single OS-native application-signing standard. SHA-256 plus GitHub/Sigstore provenance is the current release-integrity baseline. A future release can add a detached-signature policy if distribution requirements demand it.

No signing certificate, private key, Apple credential, or password belongs in the repository. Use protected GitHub environments/secrets or an external signing service.

## Candidate verification

Use the step-by-step [release checklist](RELEASE_CHECKLIST.md) for release-source validation, artifact checksum and provenance verification, real-platform smoke tests, signing disclosures, and sign-off. A successful nonpublishing dry run does not create a tag, a draft, or a published release.

## Publishing a draft

Before changing a generated draft to a normal published release:

1. verify the release workflow and ordinary CI are green;
2. verify the tag points to the intended commit and matches `package.json`;
3. run the packaged executable with `--version` and confirm version, commit, and build timestamp;
4. verify every package checksum and `SHA256SUMS.txt`;
5. verify GitHub/Sigstore provenance;
6. perform Windows signing and macOS signing/notarization when those protected release stages are available;
7. rebuild/repackage/re-attest whenever signing changes artifact bytes;
8. install and smoke-test the final packages on the target operating systems;
9. review generated release notes and asset names;
10. publish only the final reviewed assets.

If a package remains unsigned, state that clearly. Do not describe GitHub provenance as native OS signing.

## Update behavior

The current application is **notification-only**:

- it checks only the canonical public GitHub repository;
- development builds skip external update checks;
- only normal published releases can be surfaced;
- draft and prerelease responses are rejected;
- unparseable versions or malformed/untrusted release URLs fail closed;
- the UI links to the release page for manual review/install;
- no downloaded file is automatically executed.

Keeping tag-generated releases in draft form until human publication means update discovery cannot advertise a release still inside the signing/review gate.

## Rollback

Because the application does not mutate itself, rollback remains explicit:

1. stop the current executable;
2. select a previously trusted release;
3. verify its checksum, provenance, and platform signature where applicable;
4. replace the executable/package;
5. restart against the existing `DATA_DIR`.

SQLite migrations are forward-migratable inside the supported application line, but an older binary may not understand schema or behavior introduced by a newer version. Before automatic update is enabled, the project must define tested database backup/rollback semantics in addition to binary rollback.

## Criteria for automatic installation

Automatic download/install remains deferred until all of the following exist:

1. native signing/notarization for applicable platforms;
2. strict verification of the exact downloaded artifact, including checksum and platform signature/provenance;
3. platform-specific atomic replacement that never overwrites a running executable unsafely;
4. preservation of the known-good previous binary;
5. restart/health verification after replacement;
6. automatic recovery and rollback when the new process does not become healthy;
7. database migration backup/compatibility policy;
8. real Windows/macOS/Linux installation and rollback tests;
9. clear user consent, update policy, and actionable failure reporting.

Until then, release notification plus a human-reviewed GitHub Release is the safer product behavior.
