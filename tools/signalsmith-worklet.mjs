// Writes the AudioWorklet module that Signalsmith Stretch would otherwise build at run time
// and load from a blob: URL, which the TV page's Content-Security-Policy refuses. It runs the
// library's own factory against a stand-in AudioContext and keeps the module it was about to
// load, so the file is exactly the library's code.
//
//   node tools/signalsmith-worklet.mjs web/static/vendor/signalsmith-stretch.mjs \
//        web/static/vendor/signalsmith-stretch-worklet.js
import { resolveObjectURL } from 'node:buffer';
import { writeFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export async function workletModule(libraryPath) {
  const { default: SignalsmithStretch } = await import(pathToFileURL(libraryPath).href);
  let url;
  const standIn = { audioWorklet: { addModule: async (u) => { url = u; throw new Error('captured'); } } };
  try {
    await SignalsmithStretch(standIn);
  } catch {
    // the stand-in stops it there on purpose
  }
  if (!url) throw new Error('Signalsmith Stretch made no worklet module');
  return resolveObjectURL(url).text();
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [library, out] = process.argv.slice(2);
  if (!library || !out) {
    console.error('usage: node tools/signalsmith-worklet.mjs <SignalsmithStretch.mjs> <out.js>');
    process.exit(2);
  }
  writeFileSync(out, await workletModule(library));
}
