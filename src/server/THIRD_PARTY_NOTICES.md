# Third-party notices

This application includes dependencies under licenses other than the project MIT license in the repository-root `LICENSE` file. The complete reports and corresponding texts are shipped with each binary package:

- [Go dependency report](GO_THIRD_PARTY_NOTICES.md) · [Go licenses and LGPL source](licenses/go/)
- [npm production dependency report](NPM_THIRD_PARTY_NOTICES.md) · [npm license texts](licenses/npm/)
- [LGPL source and relinking instructions](LGPL_RELINKING.md)

The reports cover the Go packages used to build the executable, bundled npm production dependencies, embedded license notices within dependencies, and the Go standard library/runtime. `go-aac` is distributed under LGPL-2.1-or-later. Its source, full license text, and the materials required to rebuild the application against a modified copy are included in `licenses/go/github.com/tphakala/go-aac/` and the companion relinking guide.

The program controls a separately installed Chrome-compatible browser. No Chrome, Chromium, or Edge binary is included. The browser's license and terms continue to apply.
