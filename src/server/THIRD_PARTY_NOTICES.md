# Third-party notices

This table covers the executable's Go module dependency tree. The embedded React UI also bundles npm packages; use the root `package-lock.json` and each package's license to identify applicable notices. Preserve required notices and license terms when distributing a build, including those required by transitive Go modules.

| Component | Pinned version | Purpose | Source / license |
| --- | --- | --- | --- |
| `github.com/kkdai/youtube/v2` | `v2.10.6` | YouTube metadata and compatible direct streams | [source](https://github.com/kkdai/youtube) · [license](https://github.com/kkdai/youtube/blob/master/LICENSE) |
| `github.com/chromedp/chromedp` | `v0.14.1` | Control the installed browser for adaptive media capture | [source](https://github.com/chromedp/chromedp) · [license](https://github.com/chromedp/chromedp/blob/master/LICENSE) |
| `github.com/chromedp/cdproto` | `v0.0.0-20250724212937-08a3db8b4327` | Chrome DevTools Protocol bindings | [source](https://github.com/chromedp/cdproto) · [license](https://github.com/chromedp/cdproto/blob/master/LICENSE) |
| `github.com/yapingcat/gomedia` | `v0.0.0-20240906162731-17feea57090c` | MP4 remuxing | [source](https://github.com/yapingcat/gomedia) · [license](https://github.com/yapingcat/gomedia/blob/master/LICENSE) |
| `github.com/tphakala/go-aac` | `v0.7.0` | AAC-LC decoding for MP3 conversion | [source](https://github.com/tphakala/go-aac) · [LGPL-2.1-or-later license](licenses/go-aac-LICENSE) · [Go runtime code license](licenses/go-aac-LICENSE.golang) |
| `github.com/tphakala/go-m4a` | `v0.5.0` | MP4/M4A audio demuxing | [source](https://github.com/tphakala/go-m4a) · [MIT license](licenses/go-m4a-LICENSE) |
| `github.com/tphakala/go-mp3` | `v0.1.0` | Pure-Go MP3 encoding | [source](https://github.com/tphakala/go-mp3) · [MIT license](licenses/go-mp3-LICENSE) |
| `github.com/tphakala/simd` | `v1.9.0` | Pure-Go AAC decoder SIMD kernels | [source](https://github.com/tphakala/simd) · [MIT license](licenses/simd-LICENSE) |
| `modernc.org/sqlite` | `v1.59.0` | Pure-Go SQLite database for configuration, history, and resume state | [source](https://gitlab.com/cznic/sqlite) · [license](https://gitlab.com/cznic/sqlite/-/blob/master/LICENSE) |

The program controls a separately installed Chrome-compatible browser; no Chrome, Chromium, or Edge binary is embedded in `youtube-downloader.exe`. The browser's own license and terms continue to apply.

The AAC decoder is distributed under LGPL-2.1-or-later. When distributing the executable, provide the corresponding source and the materials required to allow users to modify the LGPL component and relink the application. The complete license text and its Go runtime attribution are included in `licenses/`.

## gomedia

~~~text
MIT License

Copyright (c) 2021 caoyaping

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is furnished
to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
~~~

## kkdai/youtube

~~~text
The MIT License (MIT)

Copyright (c) 2015 Evan Lin

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
~~~
