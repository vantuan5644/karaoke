// The song's key, for pitch correction that pulls a singer to the song's own notes rather
// than to any semitone. The server hears the key in the song's sound (GET /api/songkey); this
// asks about the song playing and the next one, and asks again while a YouTube song is
// downloading to be heard.
import { api } from './live.js';
import { keyName, notesFor } from './mic-core.js';

const RETRY_MS = 3000;

export class SongKeys {
  constructor({ onChange = () => {} } = {}) {
    this.known = new Map(); // qid -> { key, pending, error, retry, at }
    this.asking = new Set();
    this.onChange = onChange;
    this.entries = [];
  }

  // follow keeps the keys of these entries (the song playing, the next) known.
  follow(entries) {
    this.entries = entries.filter(Boolean);
    const wanted = new Set(this.entries.map((e) => e.qid));
    for (const qid of this.known.keys()) if (!wanted.has(qid)) this.known.delete(qid);
    let waiting = false;
    for (const { qid } of this.entries) {
      const k = this.known.get(qid);
      if (this.asking.has(qid)) continue;
      const again = k && (k.pending || k.retry); // still coming, or the asking itself failed
      if (again) waiting = true;
      if (k && !(again && Date.now() - k.at >= RETRY_MS)) continue;
      this.ask(qid);
    }
    clearTimeout(this.timer);
    if (waiting) this.timer = setTimeout(() => this.follow(this.entries), RETRY_MS);
  }

  // stop forgets everything and asks no more (pitch correction no longer follows the song).
  stop() {
    clearTimeout(this.timer);
    this.entries = [];
    this.known.clear();
  }

  async ask(qid) {
    this.asking.add(qid);
    let answer;
    try {
      answer = await api(`/api/songkey?qid=${encodeURIComponent(qid)}`);
    } catch (err) {
      // The server answered with what it couldn't do, or never answered (restarting, the
      // network): only the second is worth asking again.
      answer = { error: err.message, retry: !err.status };
    }
    this.asking.delete(qid);
    if (!this.entries.some((e) => e.qid === qid)) return; // gone from the queue meanwhile
    const known = { key: answer.key || null, pending: Boolean(answer.pending), error: answer.error || '', retry: Boolean(answer.retry), at: Date.now() };
    this.known.set(qid, known);
    if (known.pending || known.retry) this.follow(this.entries); // keeps the retry going
    this.onChange(qid);
  }

  get(qid) {
    return this.known.get(qid) || null;
  }
}

// shiftedKey is a key moved by a key change, as { name, tonic, minor }.
export function shiftedKey(key, semitones) {
  const tonic = (((key.tonic + Math.round(semitones || 0)) % 12) + 12) % 12;
  return { tonic, minor: key.minor, name: keyName(tonic, key.minor) };
}

// keysWanted is the entries whose keys pitch correction needs (the song playing and the
// next), or null when it isn't following the song. Each is heard from its sound, which for a
// YouTube song means downloading it, so only while someone is singing through the mic.
export function keysWanted(settings, state) {
  if (!settings.on || settings.scale !== 'song' || !settings.tune || !state?.features?.keys) return null;
  return [state.current, state.queue?.[0]];
}

// tuneNotes is what pitch correction pulls to now: the notes (12 bits, 0 for every
// semitone), what the mic panel says about them, and the song's key as it sounds. shift is
// the key change the music has now, which can lag the one asked for: an embedded video
// plays in its own key until its copy is downloaded.
export function tuneNotes(settings, state, keys, shift = state?.current?.key || 0) {
  const cur = state?.current;
  const info = cur ? keys.get(cur.qid) : null;
  return {
    notes: notesFor(settings.scale, info?.key, shift),
    text: songKeyText({ on: settings.on, scale: settings.scale, tune: settings.tune, hasKeys: Boolean(state?.features?.keys), cur, info, shift }),
    name: settings.scale === 'song' && info?.key ? shiftedKey(info.key, shift).name : '',
  };
}

// songKeyText says what pitch correction is pulling to, for the mic panel.
export function songKeyText({ on = true, scale, tune, hasKeys, cur, info, shift = cur?.key || 0 }) {
  if (scale !== 'song') return '';
  if (!hasKeys) return "This server can't hear a song's key (it needs ffmpeg), so every semitone counts for now.";
  if (!tune) return "Turn Tune up to pull your voice to the song's notes.";
  if (!on) return "The song's key is found while the mic is on.";
  if (!cur) return "The song's key is found when a song plays.";
  if (!info || info.pending) return info?.pending ? 'Getting the song ready to hear its key…' : "Listening for the song's key…";
  if (!info.key) return `${(info.error || 'No key found').replace(/\.?$/, '.')} Every semitone counts for now.`;
  const name = info.key.name;
  if (shift) return `The song is in ${name}; with its key change, ${shiftedKey(info.key, shift).name}.`;
  if (cur.key) return `The song is in ${name}. Its key change to ${shiftedKey(info.key, cur.key).name} waits for the song's copy, and Tune with it.`;
  return `The song is in ${name}: Tune pulls to its notes.`;
}
