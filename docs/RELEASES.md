# Release and update policy

The project separates **release discovery**, **build provenance**, **native platform signing**, and **installation**. They are different trust boundaries and must not be collapsed into one “auto-update” switch.

## Current release model

The application reports build metadata through `GET /api/health`, Settings, and the packaged executable:

```text
youtube-downloader --version
```

The reported identity includes:

- semantic version
- source commit
- UTC build timestamp

Direct `go build` output and packaging scripts without explicit release metadata identify themselves as `dev` / `unknown`. The tag-gated release workflow injects the exact tag, source commit, and commit timestamp.

A release build may query GitHub's public latest-release endpoint. A newer **published** release is shown as an informational link in Settings. Draft releases are invisible to that endpoint. The application does **not** download, replace, execute, or install release assets.

## Version source of truth

`package.json` is the release-version source of truth.

A release tag must be exactly:

```text
v<package.json version>
```

Example: package version `0.2.0` requires tag `v0.2.0`.

The release workflow rejects malformed tags and fails before tests or packaging if the stable tag does not match `package.json`.

## Tag release workflow

Pushing a matching stable semantic-version tag runs `.github/workflows/release.yml`.

The workflow:

1. checks out the immutable tag;
2. validates the stable tag format and exact `package.json` version match;
3. runs frontend type-check/build, Bun tests, Go tests/vet, and real-browser E2E;
4. builds Windows amd64/arm64, Linux amd64/arm64, and macOS amd64/arm64 packages;
5. embeds the same version, source commit, and build timestamp into every executable;
6. creates a tagged source archive with the LGPL decoder source and relinking guide;
7. verifies per-package SHA-256 checksums and creates `SHA256SUMS.txt`;
8. creates GitHub/Sigstore build-provenance attestations for every release archive and the checksum manifest;
9. creates a **draft** GitHub Release containing the binaries, source archive, and checksums.

The workflow intentionally stops at a draft. This prevents the application's latest-release check from advertising a tag-driven package before a human has completed the publication gate.

## Provenance verification

The release workflow uses GitHub artifact attestations backed by OIDC/Sigstore. This establishes which repository, workflow, commit, and build produced an archive.

For example:

```bash
gh attestation verify youtube-downloader-windows-amd64.zip --repo ajbergh/yt-dl-go
```

SHA-256 verification remains independent and should also be performed.

Build provenance is **not** a substitute for Windows Authenticode, Apple Developer ID signing, or Apple notarization.

## Native platform signing gate

Do not present a stable Windows/macOS release as natively signed until these platform trust steps are implemented with protected credentials.

### Windows

The final executable should be Authenticode-signed using a protected code-signing identity. Verify the Authenticode signature after signing. Because signing changes executable bytes, package checksums and provenance for the final artifact must correspond to the signed binary.

### macOS

A public macOS package should use Apple Developer ID signing and the appropriate notarization/stapling flow for its final distribution shape. If distribution moves to a `.app` bundle or DMG, the bundle hierarchy must be signed correctly before notarization.

### Linux

There is no single OS-native application-signing standard. SHA-256 plus GitHub/Sigstore provenance is the current release-integrity baseline. A future release can add a detached-signature policy if distribution requirements demand it.

No signing certificate, private key, Apple credential, or password belongs in the repository. Use protected GitHub environments/secrets or an external signing service.

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
