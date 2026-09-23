# LGPL component source and relinking

The application uses `github.com/tphakala/go-aac` v0.7.0, an LGPL-2.1-or-later AAC decoder. Its complete source is included in `licenses/go/github.com/tphakala/go-aac/` in binary packages. The source also remains available as a Go module at the exact version recorded in `go.mod` and `go.sum`.

## Modify and rebuild

1. Extract the release's `yt-dl-go-source-<version>.tar.gz` archive or check out the matching source tag.
2. Obtain the Go module sources with `cd src/server; go mod download`.
3. Modify the included source under `src/server/licenses/go/github.com/tphakala/go-aac/`, or copy it to another local directory.
4. To build against the included modified copy, add a local `replace` directive to `src/server/go.mod`, for example:

   ~~~go
   replace github.com/tphakala/go-aac => ./licenses/go/github.com/tphakala/go-aac
   ~~~

   The replacement path should point to the modified module directory containing its `go.mod`.
5. Install the pinned frontend dependencies and build the UI, then build the Go application:

   ~~~sh
   npm ci
   npm run build -- --outDir src/server/dist
   cd src/server
   go build -o youtube-downloader .
   ~~~

The build uses the application's ordinary Go source and build scripts; the resulting executable links the modified decoder implementation. Release source archives include `go.mod`, `go.sum`, the build scripts, and the LGPL decoder source materials. The decoder's license text and attribution are in `licenses/go/github.com/tphakala/go-aac/` and `licenses/go/github.com/tphakala/go-aac/LICENSE`.
