#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
server_dir="$repo_root/src/server"
target_os="${TARGET_OS:-$(go env GOOS)}"
target_arch="${TARGET_ARCH:-$(go env GOARCH)}"
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"

case "$target_os" in
  linux|darwin) ;;
  *)
    echo "build-unix.sh supports TARGET_OS=linux or darwin, got '$target_os'." >&2
    exit 1
    ;;
esac

for command in go node npm tar; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "Required command '$command' was not found on PATH." >&2
    exit 1
  }
done

if [[ ! -f "$repo_root/package.json" || ! -f "$server_dir/go.mod" ]]; then
  echo "Run this script from a complete yt-dl-go source checkout." >&2
  exit 1
fi
if [[ ! -f "$repo_root/node_modules/vite/bin/vite.js" ]]; then
  echo "Frontend dependencies are missing. Run 'npm ci' first." >&2
  exit 1
fi

output="${1:-$repo_root/dist/youtube-downloader-${target_os}-${target_arch}}"
if [[ "$output" != /* ]]; then
  output="$repo_root/$output"
fi
mkdir -p "$(dirname "$output")"

server_dist="$server_dir/dist"
if [[ -L "$server_dist" ]]; then
  echo "Refusing to write through symlink: $server_dist" >&2
  exit 1
fi
mkdir -p "$server_dist"

echo "Building embedded frontend..."
(
  cd "$repo_root"
  npm run build -- --outDir "$server_dist"
)
[[ -f "$server_dist/index.html" ]] || {
  echo "Frontend build did not produce $server_dist/index.html" >&2
  exit 1
}

echo "Downloading Go dependencies..."
(
  cd "$server_dir"
  go mod download
)

echo "Running native Go tests and vet on ${host_os}/${host_arch}..."
(
  cd "$server_dir"
  CGO_ENABLED=0 go test ./...
  CGO_ENABLED=0 go vet ./...
)

echo "Building ${target_os}/${target_arch} binary..."
(
  cd "$server_dir"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
    -buildvcs=false \
    -trimpath \
    -ldflags="-s -w" \
    -o "$output" \
    .
)

[[ -s "$output" ]] || {
  echo "Go build completed without a non-empty binary: $output" >&2
  exit 1
}
chmod +x "$output" 2>/dev/null || true

archive_dir="$(dirname "$output")"
archive_base="youtube-downloader-${target_os}-${target_arch}"
archive="$archive_dir/$archive_base.tar.gz"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/$archive_base"
cp "$output" "$stage/$archive_base/youtube-downloader"
cp "$repo_root/README.md" "$stage/$archive_base/README.md"
cp "$server_dir/THIRD_PARTY_NOTICES.md" "$stage/$archive_base/THIRD_PARTY_NOTICES.md"
cp -R "$server_dir/licenses" "$stage/$archive_base/licenses"

tar -czf "$archive" -C "$stage" "$archive_base"
if command -v shasum >/dev/null 2>&1; then
  (cd "$archive_dir" && shasum -a 256 "$(basename "$archive")" > "$(basename "$archive").sha256")
else
  (cd "$archive_dir" && sha256sum "$(basename "$archive")" > "$(basename "$archive").sha256")
fi

echo "Build succeeded: $output"
echo "Package: $archive"
echo "Checksum: $archive.sha256"
