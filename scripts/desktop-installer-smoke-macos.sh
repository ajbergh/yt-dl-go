#!/usr/bin/env bash
set -euo pipefail
[[ "${GITHUB_ACTIONS:-}" == true ]] || { echo 'Requires a disposable GitHub runner.' >&2; exit 1; }
arch=amd64
[[ "$(uname -m)" != arm64 ]] || arch=arm64
package="$PWD/dist/desktop/darwin-$arch/youtube-vault-darwin-$arch.pkg"
app='/Applications/YouTube Vault.app'
receipt=io.github.ajbergh.ytdlgo.desktop
[[ ! -e "$app" ]] || { echo 'Refusing to overwrite an existing installation.' >&2; exit 1; }
sudo installer -pkg "$package" -target /
test -x "$app/Contents/MacOS/youtube-vault"
test -s "$app/Contents/Resources/notices/THIRD_PARTY_NOTICES.md"
portable="$(mktemp -d)"
tar -xzf "dist/desktop/darwin-$arch/youtube-vault-darwin-$arch.tar.gz" -C "$portable"
cmp "$app/Contents/MacOS/youtube-vault" "$portable/youtube-vault-darwin-$arch/YouTube Vault.app/Contents/MacOS/youtube-vault"
pkgutil --pkg-info "$receipt"
sudo installer -pkg "$package" -target /
node scripts/desktop-smoke.mjs --executable "$app/Contents/MacOS/youtube-vault"
# macOS applications are removed by moving the app to Trash. No uninstall
# scripts touch the user's data. This is the exact app installed above.
sudo rm -rf -- '/Applications/YouTube Vault.app'
sudo pkgutil --forget "$receipt"
test ! -e "$app"
if pkgutil --pkg-info "$receipt" >/dev/null 2>&1; then exit 1; fi
rm -rf -- "$portable"
echo 'macOS installer, reinstallation, launch, and removal verified.'
