import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { workletModule } from '../../tools/signalsmith-worklet.mjs';

const vendor = (name) => fileURLToPath(new URL(`../static/vendor/${name}`, import.meta.url));

test('the served worklet is the one the vendored library would build', async () => {
  const built = await workletModule(vendor('signalsmith-stretch.mjs'));
  assert.equal(readFileSync(vendor('signalsmith-stretch-worklet.js'), 'utf8'), built,
    'rerun tools/signalsmith-worklet.mjs after updating the library');
});

test('keyshift.js points the library at the served worklet', () => {
  const src = readFileSync(fileURLToPath(new URL('../static/keyshift.js', import.meta.url)), 'utf8');
  assert.match(src, /moduleUrl = '\/static\/vendor\/signalsmith-stretch-worklet\.js'/);
});
