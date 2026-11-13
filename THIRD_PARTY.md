# Third-party components

Karaoke's own source and documentation use [CC BY-NC 4.0](LICENSE). Third-party
components retain their upstream licenses. The project license does not change them.
Release archives include `LICENSE`, this file and the complete notices in `LICENSES/`;
the container installs them under `/usr/share/licenses/karaoke/`.

## Go dependencies

| Component | Use | License text |
| --- | --- | --- |
| [rsc.io/qr v0.2.0](https://pkg.go.dev/rsc.io/qr@v0.2.0) | QR code generation | [BSD-3-Clause](LICENSES/rsc.io-qr.txt) |
| [golang.org/x/text v0.44.0](https://pkg.go.dev/golang.org/x/text@v0.44.0) | Accent folding and Unicode text | [BSD-3-Clause](LICENSES/golang.org-x-text.txt), [patent grant](LICENSES/golang-PATENTS.txt) |
| [Go runtime and standard library](https://go.dev/) | Compiled into the executable | [BSD-3-Clause](LICENSES/Go.txt) |

`go.mod` and `go.sum` record module versions and checksums. Module source is fetched by
Go during a build; it is not copied into this repository.

## Signalsmith Stretch 1.3.2

`web/static/vendor/signalsmith-stretch.mjs` is `SignalsmithStretch.mjs` from the npm
package [signalsmith-stretch](https://www.npmjs.com/package/signalsmith-stretch/v/1.3.2),
the official JS/WASM release of [Signalsmith Stretch](https://signalsmith-audio.co.uk/code/stretch/)
by Geraint Luff, unchanged. The TV page uses it to change a song's key.

`web/static/vendor/signalsmith-stretch-worklet.js` is the AudioWorklet module the library
builds at runtime, saved as a file because the page's Content-Security-Policy does not
allow the `blob:` URL the library would load it from. Regenerate it with:

```sh
node tools/signalsmith-worklet.mjs web/static/vendor/signalsmith-stretch.mjs \
     web/static/vendor/signalsmith-stretch-worklet.js
```

`web/test/keyshift.test.mjs` checks that the generated file still matches the library.
Both files retain the [MIT license](LICENSES/signalsmith-stretch.txt), copyright Geraint
Luff / Signalsmith Audio Ltd.

## cdgraphics 7.0.0

`web/static/vendor/cdgraphics.js` is `dist/cdgraphics.js` from the npm package
[cdgraphics](https://www.npmjs.com/package/cdgraphics/v/7.0.0)
([source](https://github.com/bhj/cdgraphics)), unchanged. It renders MP3+G graphics.
The package metadata says ISC; its bundled `LICENSE` contains the MIT permission text.
[LICENSES/cdgraphics.txt](LICENSES/cdgraphics.txt) preserves that text and its copyright
notices for Luke Tucker, Keith McKnight and RadRoot LLC verbatim.

## Algorithms

`internal/voice/dsp.go` follows Freeverb by Jezar at Dreampoint (public domain): eight
damped comb filters and four all-pass filters per side, with its delay-line tunings.
No Freeverb code is copied. The pitch detector is an implementation of YIN
(de Cheveigne and Kawahara, 2002), written from the paper.

## Container tools and media

The Dockerfile also installs Debian packages, ffmpeg, yt-dlp and Deno, each under its
own license. Debian package notices remain under `/usr/share/doc/`. Consult
[yt-dlp's distribution licensing](https://github.com/yt-dlp/yt-dlp#licensing),
[Deno's license](https://github.com/denoland/deno/blob/main/LICENSE.md) and
[FFmpeg's legal information](https://ffmpeg.org/legal.html) when redistributing an image.
In particular, yt-dlp's standalone Linux executables include GPLv3+ components.
The image's CC-BY-NC-4.0 label describes Karaoke's own code.

Songs, videos, lyrics, thumbnails and user-provided media retain their respective
rights holders' terms. They are not licensed by this repository's CC BY-NC license.
