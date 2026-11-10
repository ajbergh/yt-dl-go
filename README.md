<p align="center">
  <img src="public/app-icon.svg" alt="YouTube Vault" width="88" />
</p>

# YouTube Vault

**Your ultimate YouTube downloader.**

Save the videos you love. Build a collection worth keeping. YouTube Vault brings
powerful video and playlist downloads, flexible audio options, and a searchable
media library together in one desktop app for **Windows, macOS, and Linux**.

From a single clip to a full playlist, take control of quality, format, and every
step of your queue. Then watch, listen, and find your favorites in a Vault that
keeps your media close at hand.

**[Download v0.4.0](#get-the-app)** · **[See what's new](https://github.com/ajbergh/yt-dl-go/releases/tag/v0.4.0)** · **[Get started](#from-link-to-library)**

## Built for your collection

- **Great video. Great audio.** Choose available video quality up to 4K, save MP3 or M4A audio, and include available captions.
- **Playlists, your way.** Download a whole playlist or hand-pick the entries you want. Inspect multiple links and set up a batch in one place.
- **Stay in control.** Follow live progress, speed, and time remaining. Pause, resume, reorder, and retry with a queue that remembers your work.
- **A Vault you'll want to use.** Browse in a grid or list, search across saved media, and filter by type, category, or creator. Favorites and personal collections make it easy to find what matters.
- **Press play, right here.** Preview saved video and audio inside the app, seek through supported media, and jump straight to its location on disk.
- **Fast where it counts.** Parallel downloads, reusable audio tracks, and separate MP3 conversion queues keep your collection growing. Finished playlist tracks are ready while the rest continue.
- **Your files, under your control.** Choose your download folder and naming rules. Organize your Vault without moving your files, and manage individual files with clear confirmation dialogs.

## See it in action

### Your queue, at a glance

Every playlist entry gets its own progress, speed, and status. See what's moving,
what's next, and what's ready to enjoy.

![Download queue showing individual playlist downloads, active and queued counts, and per-download progress](docs/assets/screenshots/Queue-screenshot1.png)

### A home for every download

Turn a folder of files into a collection you can browse. Find a favorite, explore
a creator's videos, or open a file's details without losing your place.

![Media Vault showing the media grid, navigation sidebar, filters, and selected-file details](docs/assets/screenshots/Vault-screenshot1.png)

### Ready when you are

Watch and listen to saved media inside your Vault. Your downloaded files are
available for offline playback.

![Local video preview open over the Media Vault](docs/assets/screenshots/Preview-screenshot1.png)

## Get the app

Choose **AMD64** for Intel/AMD computers or **ARM64** for Windows on ARM and Apple
Silicon. Download **v0.4.0** directly below, with both portable packages and
installers available:

| Platform | Portable package | Installer |
| --- | --- | --- |
| Windows 10/11 — AMD64 | [EXE](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-windows-amd64.exe) and [ZIP with notices](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-windows-amd64.zip) | [Setup EXE](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-windows-amd64-setup.exe) |
| Windows 10/11 — ARM64 | [EXE](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-windows-arm64.exe) and [ZIP with notices](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-windows-arm64.zip) | [Setup EXE](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-windows-arm64-setup.exe) |
| macOS — Intel (AMD64) | [App archive (.tar.gz)](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-darwin-amd64.tar.gz), containing `YouTube Vault.app` | [PKG](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-darwin-amd64.pkg) |
| macOS — Apple Silicon (ARM64) | [App archive (.tar.gz)](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-darwin-arm64.tar.gz), containing `YouTube Vault.app` | [PKG](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-darwin-arm64.pkg) |
| Linux — AMD64 | [Portable archive (.tar.gz)](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-linux-amd64.tar.gz) | [DEB](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-linux-amd64.deb) |
| Linux — ARM64 | [Portable archive (.tar.gz)](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-linux-arm64.tar.gz) | [DEB](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/youtube-vault-linux-arm64.deb) |

- **Windows:** choose Setup for an installer with shortcuts and an uninstaller, or use the portable EXE. Keep the companion ZIP with its notices when sharing the portable app. Setup installs the required WebView2 runtime when missing and needs internet access for that step.
- **macOS:** use the PKG to install into Applications, or extract the app archive and open YouTube Vault.
- **Linux:** use the DEB on supported Debian/Ubuntu systems, or choose the portable archive. See the [installation and runtime requirements](docs/DESKTOP.md) for compatible system libraries.

Every download has a SHA-256 checksum. Verify files with the
[checksum manifest](https://github.com/ajbergh/yt-dl-go/releases/download/v0.4.0/SHA256SUMS.txt),
and read the [release notes](https://github.com/ajbergh/yt-dl-go/releases/tag/v0.4.0)
for verification instructions and platform support. Windows packages are not
Authenticode-signed; macOS packages are not Developer ID-signed or notarized.

The release page also includes the browser/server edition and source archives.

## From link to library

1. **Paste a link.** Add a video or playlist URL and choose **View options**. Use **Multiple links** to prepare a batch.
2. **Make it yours.** Choose video or audio, your maximum quality, available captions, and the playlist entries you want.
3. **Start your queue.** Confirm you have permission to download the content, then add it to **Queue**. Pause, resume, reorder, or retry as needed.
4. **Enjoy your Vault.** Browse, search, favorite, and organize your saved media. Select a card for details, preview it, or reveal the file on disk.

Right-click downloads, media cards, or collections for useful shortcuts. Enable
system notifications in Settings to get alerts when downloads finish or fail.
Your queue, preferences, and Vault are saved between launches.

## Your media, your space

Your download folder is your Vault. By default, files are saved to
`~/Downloads/YouTube_Vault` and organized by channel. Choose another folder,
customize file names, and set your preferred formats in Settings.

Favorites and collections help you organize files in place. **Clear completed**
tidies the queue while preserving your media. **Forget item** removes a Vault
entry while keeping its file. **Delete selected file** and **Delete entire
download** show confirmations for their respective deletion scopes.

Video quality follows the available source streams and your selected maximum
resolution. For browser-assisted HD/4K downloads, install Chrome, Chromium, or
Microsoft Edge. See the [format and runtime guide](docs/TECHNICAL_GUIDE.md#media-formats-and-browser-assisted-capture)
for details.

## Use responsibly

Download only content you own or have permission to save, in accordance with
YouTube's terms. DRM, private-account credentials, cookie imports, paywall
bypasses, and live-stream recording are unsupported.

## Explore the project

- [Architecture, source builds, and troubleshooting](docs/TECHNICAL_GUIDE.md)
- [Desktop installation and platform requirements](docs/DESKTOP.md)
- [What's new](CHANGELOG.md) and [release notes](https://github.com/ajbergh/yt-dl-go/releases/tag/v0.4.0)
- [Contributing](CONTRIBUTING.md), [security](SECURITY.md), and [roadmap](roadmap.md)

## License

MIT for the project; third-party components retain their licenses. Distributions
include dependency notices, license texts, LGPL decoder source, and relinking
materials. Tagged releases include the corresponding project source archive.
See [third-party notices](src/server/THIRD_PARTY_NOTICES.md) and
[LGPL relinking instructions](src/server/LGPL_RELINKING.md).
