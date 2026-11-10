import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SongKeys, keysWanted, shiftedKey, songKeyText, tuneNotes } from '../static/tv-songkey.js';
import { majorNotes } from '../static/mic-core.js';

const A_MINOR = { tonic: 9, minor: true, name: 'A minor', notes: majorNotes(0) };

// answering fakes GET /api/songkey: replies(qid) is the answer for that ask.
function answering(replies) {
  const asked = [];
  globalThis.fetch = async (path) => {
    const qid = new URL(path, 'http://tv').searchParams.get('qid');
    asked.push(qid);
    return { ok: true, status: 200, json: async () => replies(qid, asked.length) };
  };
  return asked;
}

const settle = () => new Promise((r) => setImmediate(r)); // after the fake fetch's promises

test('SongKeys asks about the song playing and the next one, once each', async () => {
  const asked = answering((qid) => ({ qid, key: A_MINOR }));
  const changed = [];
  const keys = new SongKeys({ onChange: (qid) => changed.push(qid) });
  keys.follow([{ qid: 'a' }, { qid: 'b' }]);
  await settle();
  keys.follow([{ qid: 'a' }, { qid: 'b' }, undefined]);
  await settle();
  assert.deepEqual(asked, ['a', 'b']);
  assert.deepEqual(changed, ['a', 'b']);
  assert.equal(keys.get('a').key.name, 'A minor');
  // Songs that have gone are forgotten.
  keys.follow([{ qid: 'b' }]);
  assert.equal(keys.get('a'), null);
  keys.stop();
});

test('SongKeys asks again while the song downloads', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
  const asked = answering((qid, n) => (n < 2 ? { qid, pending: true } : { qid, key: A_MINOR }));
  const keys = new SongKeys();
  keys.follow([{ qid: 'a' }]);
  await settle();
  assert.equal(keys.get('a').pending, true);
  t.mock.timers.tick(2999);
  await settle();
  assert.deepEqual(asked, ['a'], 'asked again too soon');
  t.mock.timers.tick(1);
  await settle();
  assert.deepEqual(asked, ['a', 'a']);
  assert.equal(keys.get('a').key.name, 'A minor');
  keys.stop();
});

test('SongKeys keeps an error as the answer', async () => {
  globalThis.fetch = async () => ({ ok: false, status: 404, statusText: 'Not Found', json: async () => ({ error: 'no such entry' }) });
  const keys = new SongKeys();
  keys.follow([{ qid: 'a' }]);
  await settle();
  assert.equal(keys.get('a').key, null);
  assert.equal(keys.get('a').error, 'no such entry');
  keys.stop();
});

test('keysWanted only while Tune follows the song', () => {
  const state = { features: { keys: true }, current: { qid: 'a' }, queue: [{ qid: 'b' }] };
  assert.deepEqual(keysWanted({ scale: 'song', tune: 50 }, state).map((e) => e.qid), ['a', 'b']);
  assert.equal(keysWanted({ scale: 'song', tune: 0 }, state), null);
  assert.equal(keysWanted({ scale: 'chromatic', tune: 50 }, state), null);
  assert.equal(keysWanted({ scale: 'song', tune: 50 }, { ...state, features: {} }), null);
});

test('a key change moves the song key and its notes', () => {
  assert.equal(shiftedKey(A_MINOR, 2).name, 'B minor');
  assert.equal(shiftedKey(A_MINOR, -9).name, 'C minor');
  const keys = { get: () => ({ key: A_MINOR }) };
  const state = { features: { keys: true }, current: { qid: 'a', key: 2 } };
  const now = tuneNotes({ scale: 'song', tune: 60 }, state, keys);
  assert.equal(now.notes, majorNotes(2)); // A minor up two is B minor: D major's notes
  assert.equal(now.name, 'B minor');
  assert.match(now.text, /A minor.*B minor/);
  // Every semitone ignores the song's key; a hand-picked key moves with the key change.
  assert.equal(tuneNotes({ scale: 'chromatic', tune: 60 }, state, keys).notes, 0);
  assert.equal(tuneNotes({ scale: 'key-0', tune: 60 }, state, keys).notes, majorNotes(2));
  assert.equal(tuneNotes({ scale: 'key-0', tune: 60 }, state, keys).name, '');
});

test('songKeyText says why there is no key yet', () => {
  const base = { scale: 'song', tune: 50, hasKeys: true, cur: { qid: 'a' } };
  assert.equal(songKeyText({ ...base, scale: 'chromatic' }), '');
  assert.match(songKeyText({ ...base, hasKeys: false }), /ffmpeg/);
  assert.match(songKeyText({ ...base, tune: 0 }), /Turn Tune up/);
  assert.match(songKeyText({ ...base, cur: null }), /when a song plays/);
  assert.match(songKeyText({ ...base, info: null }), /Listening/);
  assert.match(songKeyText({ ...base, info: { pending: true } }), /Downloading/);
  assert.match(songKeyText({ ...base, info: { key: null, error: 'Download failed: 403' } }), /403.*Every semitone/);
  assert.match(songKeyText({ ...base, info: { key: A_MINOR } }), /in A minor/);
});
