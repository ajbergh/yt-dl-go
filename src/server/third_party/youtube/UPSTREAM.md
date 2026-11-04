# Local compatibility patch

Source: github.com/kkdai/youtube/v2, v2.10.6, MIT license (retained in LICENSE).
Production Go sources and module manifests are copied unchanged except for:

- `response_data.go`: retain `xtags`, `isDrc`, and `isVb` in Format.
- `client.go`: add `SetClientProfile`, which copies a profile into the client
  instance instead of relying on mutable package-global DefaultClient state.

The application still creates separate client instances for operations. It
caches public player assets and short-lived, independently copied metadata
above this module. No global default is changed around a request, and no
client instance is shared between active streams with different profiles.

Keep this patch small when updating upstream. Application regression tests
exercise synthetic player responses, variant preservation and profile isolation.
