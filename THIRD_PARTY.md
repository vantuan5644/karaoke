# Third-party code

## Signalsmith Stretch 1.3.2

`web/static/vendor/signalsmith-stretch.mjs` is `SignalsmithStretch.mjs` from the npm package
[`signalsmith-stretch`](https://www.npmjs.com/package/signalsmith-stretch) 1.3.2, the official
JS/WASM release of [Signalsmith Stretch](https://signalsmith-audio.co.uk/code/stretch/) by
Geraint Luff, unchanged. The TV page uses it to change a song's key.

`web/static/vendor/signalsmith-stretch-worklet.js` is the AudioWorklet module the library
builds at run time, saved as a file because the page's Content-Security-Policy does not allow
the `blob:` URL the library would load it from. It is made from the library, not edited:

```sh
node tools/signalsmith-worklet.mjs web/static/vendor/signalsmith-stretch.mjs \
     web/static/vendor/signalsmith-stretch-worklet.js
```

`web/test/keyshift.test.mjs` fails if the two drift apart.

Its license, as stated by the package (`"license": "MIT"`, author Geraint Luff):

```
MIT License

Copyright (c) Geraint Luff / Signalsmith Audio Ltd.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
